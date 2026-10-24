package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/config"
)

func clearGatewayEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"GATEWAY_HTTP_ADDR", "GATEWAY_AUTH_GRPC_ADDR", "GATEWAY_CHAT_GRPC_ADDR", "GATEWAY_REDIS_ADDR", "GATEWAY_REDIS_PASSWORD",
		"GATEWAY_TLS_CERT_PATH", "GATEWAY_TLS_KEY_PATH", "GATEWAY_CORS_ALLOWED_ORIGINS",
		"GATEWAY_MAX_BODY_BYTES", "GATEWAY_REQUEST_TIMEOUT", "GATEWAY_RATE_LIMIT_PER_MINUTE",
		"GATEWAY_RATE_LIMIT_BURST", "GATEWAY_LOG_LEVEL", "GATEWAY_WS_MAX_CONNECTIONS_PER_USER", "GATEWAY_WS_SEND_BUFFER",
		"GATEWAY_WS_AUTH_TIMEOUT", "GATEWAY_WS_PING_INTERVAL", "GATEWAY_WS_WRITE_TIMEOUT",
		"GATEWAY_WS_MAX_CONNECTIONS", "GATEWAY_WS_MAX_UNAUTHENTICATED_CONNECTIONS",
		"GATEWAY_AUTH_RATE_LIMIT_PER_MINUTE", "GATEWAY_AUTH_RATE_LIMIT_BURST",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadGateway(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		clearGatewayEnv(t)

		cfg, err := config.LoadGateway()
		require.NoError(t, err)
		require.Equal(t, ":8080", cfg.HTTPAddr)
		require.Equal(t, "localhost:50051", cfg.AuthGRPCAddr)
		require.Equal(t, "localhost:50052", cfg.ChatGRPCAddr)
		require.Equal(t, "localhost:6379", cfg.RedisAddr)
		require.Empty(t, cfg.RedisPassword)
		require.Empty(t, cfg.TLSCertPath)
		require.Empty(t, cfg.TLSKeyPath)
		require.Empty(t, cfg.CORSAllowedOrigins)
		require.Equal(t, int64(1<<20), cfg.MaxBodyBytes)
		require.Equal(t, 10*time.Second, cfg.RequestTimeout)
		require.Equal(t, uint32(120), cfg.RateLimitPerMinute)
		require.Equal(t, uint32(30), cfg.RateLimitBurst)
		require.Equal(t, uint32(5), cfg.AuthRateLimitPerMinute)
		require.Equal(t, uint32(5), cfg.AuthRateLimitBurst)
		require.Equal(t, "info", cfg.LogLevel)
		require.Equal(t, config.WebSocketConfig{
			MaxConnectionsPerUser: 5, SendBuffer: 64, AuthTimeout: 5 * time.Second, PingInterval: 30 * time.Second, WriteTimeout: 10 * time.Second,
			MaxConnections: 10000, MaxUnauthenticatedConnections: 1000,
		}, cfg.WebSocket)
	})

	t.Run("explicit values win", func(t *testing.T) {
		clearGatewayEnv(t)
		t.Setenv("GATEWAY_HTTP_ADDR", ":9090")
		t.Setenv("GATEWAY_AUTH_GRPC_ADDR", "auth:50051")
		t.Setenv("GATEWAY_CHAT_GRPC_ADDR", "chat:50052")
		t.Setenv("GATEWAY_REDIS_ADDR", "redis:6379")
		t.Setenv("GATEWAY_REDIS_PASSWORD", "secret")
		t.Setenv("GATEWAY_TLS_CERT_PATH", "secrets/gateway.crt")
		t.Setenv("GATEWAY_TLS_KEY_PATH", "secrets/gateway.key")
		t.Setenv("GATEWAY_CORS_ALLOWED_ORIGINS", " https://chat.example.com , http://localhost:3000 ")
		t.Setenv("GATEWAY_MAX_BODY_BYTES", "4096")
		t.Setenv("GATEWAY_REQUEST_TIMEOUT", "3s")
		t.Setenv("GATEWAY_RATE_LIMIT_PER_MINUTE", "600")
		t.Setenv("GATEWAY_RATE_LIMIT_BURST", "50")
		t.Setenv("GATEWAY_AUTH_RATE_LIMIT_PER_MINUTE", "20")
		t.Setenv("GATEWAY_AUTH_RATE_LIMIT_BURST", "10")
		t.Setenv("GATEWAY_LOG_LEVEL", "debug")
		t.Setenv("GATEWAY_WS_MAX_CONNECTIONS_PER_USER", "2")
		t.Setenv("GATEWAY_WS_SEND_BUFFER", "16")
		t.Setenv("GATEWAY_WS_AUTH_TIMEOUT", "2s")
		t.Setenv("GATEWAY_WS_PING_INTERVAL", "15s")
		t.Setenv("GATEWAY_WS_WRITE_TIMEOUT", "4s")
		t.Setenv("GATEWAY_WS_MAX_CONNECTIONS", "500")
		t.Setenv("GATEWAY_WS_MAX_UNAUTHENTICATED_CONNECTIONS", "500")

		cfg, err := config.LoadGateway()
		require.NoError(t, err)
		require.Equal(t, ":9090", cfg.HTTPAddr)
		require.Equal(t, "auth:50051", cfg.AuthGRPCAddr)
		require.Equal(t, "chat:50052", cfg.ChatGRPCAddr)
		require.Equal(t, "redis:6379", cfg.RedisAddr)
		require.Equal(t, "secret", cfg.RedisPassword)
		require.Equal(t, "secrets/gateway.crt", cfg.TLSCertPath)
		require.Equal(t, "secrets/gateway.key", cfg.TLSKeyPath)
		require.Equal(t, []string{"https://chat.example.com", "http://localhost:3000"}, cfg.CORSAllowedOrigins)
		require.Equal(t, int64(4096), cfg.MaxBodyBytes)
		require.Equal(t, 3*time.Second, cfg.RequestTimeout)
		require.Equal(t, uint32(600), cfg.RateLimitPerMinute)
		require.Equal(t, uint32(50), cfg.RateLimitBurst)
		require.Equal(t, uint32(20), cfg.AuthRateLimitPerMinute)
		require.Equal(t, uint32(10), cfg.AuthRateLimitBurst)
		require.Equal(t, "debug", cfg.LogLevel)
		require.Equal(t, config.WebSocketConfig{
			MaxConnectionsPerUser: 2, SendBuffer: 16, AuthTimeout: 2 * time.Second, PingInterval: 15 * time.Second, WriteTimeout: 4 * time.Second,
			MaxConnections: 500, MaxUnauthenticatedConnections: 500,
		}, cfg.WebSocket)
	})

	t.Run("unauthenticated WebSocket limit follows a lower total", func(t *testing.T) {
		clearGatewayEnv(t)
		t.Setenv("GATEWAY_WS_MAX_CONNECTIONS", "100")

		cfg, err := config.LoadGateway()
		require.NoError(t, err)
		require.Equal(t, uint32(100), cfg.WebSocket.MaxConnections)
		require.Equal(t, uint32(100), cfg.WebSocket.MaxUnauthenticatedConnections)
	})

	t.Run("wildcard origin", func(t *testing.T) {
		clearGatewayEnv(t)
		t.Setenv("GATEWAY_CORS_ALLOWED_ORIGINS", "*")

		cfg, err := config.LoadGateway()
		require.NoError(t, err)
		require.Equal(t, []string{"*"}, cfg.CORSAllowedOrigins)
	})

	t.Run("TLS requires both certificate and key", func(t *testing.T) {
		for _, key := range []string{"GATEWAY_TLS_CERT_PATH", "GATEWAY_TLS_KEY_PATH"} {
			t.Run(key, func(t *testing.T) {
				clearGatewayEnv(t)
				t.Setenv(key, "secrets/only-one.pem")

				_, err := config.LoadGateway()
				require.ErrorContains(t, err, "GATEWAY_TLS_CERT_PATH")
			})
		}
	})
}

func TestLoadGateway_InvalidValues(t *testing.T) {
	cases := map[string][]string{
		"GATEWAY_MAX_BODY_BYTES":             {"0", "-1", "invalid", "4294967296"},
		"GATEWAY_REQUEST_TIMEOUT":            {"0s", "-1s", "soon"},
		"GATEWAY_RATE_LIMIT_PER_MINUTE":      {"0", "-1", "many"},
		"GATEWAY_RATE_LIMIT_BURST":           {"0", "-1", "many"},
		"GATEWAY_AUTH_RATE_LIMIT_PER_MINUTE": {"0", "-1", "many", "65536"},
		"GATEWAY_AUTH_RATE_LIMIT_BURST":      {"0", "-1", "many", "65536"},
		// Per-connection queues are allocated upfront, so they are bounded.
		"GATEWAY_WS_MAX_CONNECTIONS_PER_USER": {"0", "-1", "65536"},
		"GATEWAY_WS_SEND_BUFFER":              {"0", "many", "65536"},
		"GATEWAY_WS_AUTH_TIMEOUT":             {"0s", "soon"},
		"GATEWAY_WS_PING_INTERVAL":            {"-1s", "often"},
		"GATEWAY_WS_WRITE_TIMEOUT":            {"0s", "never"},
		// The unauthenticated limit may not exceed the default total of 10000.
		"GATEWAY_WS_MAX_CONNECTIONS":                 {"0", "-1", "many", "16777216"},
		"GATEWAY_WS_MAX_UNAUTHENTICATED_CONNECTIONS": {"0", "-1", "many", "10001"},
		"GATEWAY_CORS_ALLOWED_ORIGINS": {
			"chat.example.com",
			"ftp://chat.example.com",
			"https://chat.example.com/",
			"https://chat.example.com/path",
			"https://chat.example.com?x=1",
			"https://user@chat.example.com",
			"https://",
			"*,https://chat.example.com",
		},
	}
	for key, values := range cases {
		for _, value := range values {
			t.Run(key+"/"+value, func(t *testing.T) {
				clearGatewayEnv(t)
				t.Setenv(key, value)

				_, err := config.LoadGateway()
				require.ErrorContains(t, err, key)
			})
		}
	}
}
