package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// GatewayConfig holds the API gateway configuration. See .env.example for the
// full list of GATEWAY_* variables.
type GatewayConfig struct {
	HTTPAddr           string
	AuthGRPCAddr       string
	ChatGRPCAddr       string
	MediaGRPCAddr      string
	RedisAddr          string
	RedisPassword      string
	TLSCertPath        string
	TLSKeyPath         string
	CORSAllowedOrigins []string
	MaxBodyBytes       int64
	RequestTimeout     time.Duration
	RateLimitPerMinute uint32
	RateLimitBurst     uint32
	LogLevel           string
	WebSocket          WebSocketConfig
}

// WebSocketConfig holds the limits of real-time connections (GATEWAY_WS_*).
type WebSocketConfig struct {
	// MaxConnectionsPerUser is enforced per Gateway instance.
	MaxConnectionsPerUser uint32
	// SendBuffer is the number of events queued per connection before a
	// client that does not keep up is disconnected.
	SendBuffer   uint32
	AuthTimeout  time.Duration
	PingInterval time.Duration
	WriteTimeout time.Duration
	// MaxConnections bounds the connections of one Gateway instance; further
	// upgrades get 503. It holds even while the Redis rate limiter fails open.
	MaxConnections uint32
	// MaxUnauthenticatedConnections bounds the connections that have yet to
	// send a valid token, so that they cannot take every slot. It defaults to
	// 1000 or MaxConnections, whichever is lower, and may not exceed it.
	MaxUnauthenticatedConnections uint32
}

// LoadGateway reads configuration from GATEWAY_* environment variables. All
// variables have development defaults; TLS is enabled only when both the
// certificate and the key path are set.
func LoadGateway() (*GatewayConfig, error) {
	cfg := &GatewayConfig{
		MediaGRPCAddr: os.Getenv("GATEWAY_MEDIA_GRPC_ADDR"),
		HTTPAddr:      envOr("GATEWAY_HTTP_ADDR", ":8080"),
		AuthGRPCAddr:  envOr("GATEWAY_AUTH_GRPC_ADDR", "localhost:50051"),
		ChatGRPCAddr:  envOr("GATEWAY_CHAT_GRPC_ADDR", "localhost:50052"),
		RedisAddr:     envOr("GATEWAY_REDIS_ADDR", "localhost:6379"),
		RedisPassword: os.Getenv("GATEWAY_REDIS_PASSWORD"),
		TLSCertPath:   os.Getenv("GATEWAY_TLS_CERT_PATH"),
		TLSKeyPath:    os.Getenv("GATEWAY_TLS_KEY_PATH"),
		LogLevel:      envOr("GATEWAY_LOG_LEVEL", "info"),
	}

	if (cfg.TLSCertPath == "") != (cfg.TLSKeyPath == "") {
		return nil, errors.New("environment variables GATEWAY_TLS_CERT_PATH and GATEWAY_TLS_KEY_PATH must be set together")
	}

	var err error
	if cfg.CORSAllowedOrigins, err = parseOrigins(os.Getenv("GATEWAY_CORS_ALLOWED_ORIGINS")); err != nil {
		return nil, fmt.Errorf("environment variable GATEWAY_CORS_ALLOWED_ORIGINS: %w", err)
	}
	maxBodyBytes, err := uintEnv("GATEWAY_MAX_BODY_BYTES", 1<<20, 32)
	if err != nil {
		return nil, err
	}
	cfg.MaxBodyBytes = int64(maxBodyBytes)
	if cfg.RequestTimeout, err = durationEnv("GATEWAY_REQUEST_TIMEOUT", 10*time.Second); err != nil {
		return nil, err
	}
	if cfg.RateLimitPerMinute, err = uintEnv("GATEWAY_RATE_LIMIT_PER_MINUTE", 120, 32); err != nil {
		return nil, err
	}
	if cfg.RateLimitBurst, err = uintEnv("GATEWAY_RATE_LIMIT_BURST", 30, 32); err != nil {
		return nil, err
	}
	if cfg.WebSocket, err = loadWebSocket(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func loadWebSocket() (WebSocketConfig, error) {
	ws := WebSocketConfig{}
	var err error
	if ws.MaxConnectionsPerUser, err = uintEnv("GATEWAY_WS_MAX_CONNECTIONS_PER_USER", 5, 16); err != nil {
		return ws, err
	}
	if ws.SendBuffer, err = uintEnv("GATEWAY_WS_SEND_BUFFER", 64, 16); err != nil {
		return ws, err
	}
	if ws.AuthTimeout, err = durationEnv("GATEWAY_WS_AUTH_TIMEOUT", 5*time.Second); err != nil {
		return ws, err
	}
	if ws.PingInterval, err = durationEnv("GATEWAY_WS_PING_INTERVAL", 30*time.Second); err != nil {
		return ws, err
	}
	if ws.WriteTimeout, err = durationEnv("GATEWAY_WS_WRITE_TIMEOUT", 10*time.Second); err != nil {
		return ws, err
	}
	// 24 bits keep the limits within int on every platform.
	if ws.MaxConnections, err = uintEnv("GATEWAY_WS_MAX_CONNECTIONS", 10000, 24); err != nil {
		return ws, err
	}
	if ws.MaxUnauthenticatedConnections, err = uintEnv("GATEWAY_WS_MAX_UNAUTHENTICATED_CONNECTIONS", min(1000, ws.MaxConnections), 24); err != nil {
		return ws, err
	}
	if ws.MaxUnauthenticatedConnections > ws.MaxConnections {
		return ws, fmt.Errorf("environment variable GATEWAY_WS_MAX_UNAUTHENTICATED_CONNECTIONS must not exceed GATEWAY_WS_MAX_CONNECTIONS (%d)", ws.MaxConnections)
	}
	return ws, nil
}

// parseOrigins parses a comma-separated list of CORS origins. Each origin must
// be exactly scheme://host[:port] with an http or https scheme, because that
// is what browsers send in the Origin header; a lone "*" allows any origin.
func parseOrigins(value string) ([]string, error) {
	var origins []string
	for _, part := range strings.Split(value, ",") {
		origin := strings.TrimSpace(part)
		if origin == "" {
			continue
		}
		if origin != "*" {
			parsed, err := url.Parse(origin)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
				parsed.Host == "" || parsed.Scheme+"://"+parsed.Host != origin {
				return nil, fmt.Errorf("invalid origin %q: want scheme://host[:port]", origin)
			}
		}
		origins = append(origins, origin)
	}

	for _, origin := range origins {
		if origin == "*" && len(origins) > 1 {
			return nil, errors.New(`"*" cannot be combined with explicit origins`)
		}
	}
	return origins, nil
}
