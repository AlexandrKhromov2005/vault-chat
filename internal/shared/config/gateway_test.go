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
		"GATEWAY_HTTP_ADDR", "GATEWAY_AUTH_GRPC_ADDR", "GATEWAY_REDIS_ADDR", "GATEWAY_REDIS_PASSWORD",
		"GATEWAY_TLS_CERT_PATH", "GATEWAY_TLS_KEY_PATH", "GATEWAY_CORS_ALLOWED_ORIGINS",
		"GATEWAY_MAX_BODY_BYTES", "GATEWAY_REQUEST_TIMEOUT", "GATEWAY_RATE_LIMIT_PER_MINUTE",
		"GATEWAY_RATE_LIMIT_BURST", "GATEWAY_LOG_LEVEL",
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
		require.Equal(t, "localhost:6379", cfg.RedisAddr)
		require.Empty(t, cfg.RedisPassword)
		require.Empty(t, cfg.TLSCertPath)
		require.Empty(t, cfg.TLSKeyPath)
		require.Empty(t, cfg.CORSAllowedOrigins)
		require.Equal(t, int64(1<<20), cfg.MaxBodyBytes)
		require.Equal(t, 10*time.Second, cfg.RequestTimeout)
		require.Equal(t, uint32(120), cfg.RateLimitPerMinute)
		require.Equal(t, uint32(30), cfg.RateLimitBurst)
		require.Equal(t, "info", cfg.LogLevel)
	})

	t.Run("explicit values win", func(t *testing.T) {
		clearGatewayEnv(t)
		t.Setenv("GATEWAY_HTTP_ADDR", ":9090")
		t.Setenv("GATEWAY_AUTH_GRPC_ADDR", "auth:50051")
		t.Setenv("GATEWAY_REDIS_ADDR", "redis:6379")
		t.Setenv("GATEWAY_REDIS_PASSWORD", "secret")
		t.Setenv("GATEWAY_TLS_CERT_PATH", "secrets/gateway.crt")
		t.Setenv("GATEWAY_TLS_KEY_PATH", "secrets/gateway.key")
		t.Setenv("GATEWAY_CORS_ALLOWED_ORIGINS", " https://chat.example.com , http://localhost:3000 ")
		t.Setenv("GATEWAY_MAX_BODY_BYTES", "4096")
		t.Setenv("GATEWAY_REQUEST_TIMEOUT", "3s")
		t.Setenv("GATEWAY_RATE_LIMIT_PER_MINUTE", "600")
		t.Setenv("GATEWAY_RATE_LIMIT_BURST", "50")
		t.Setenv("GATEWAY_LOG_LEVEL", "debug")

		cfg, err := config.LoadGateway()
		require.NoError(t, err)
		require.Equal(t, ":9090", cfg.HTTPAddr)
		require.Equal(t, "auth:50051", cfg.AuthGRPCAddr)
		require.Equal(t, "redis:6379", cfg.RedisAddr)
		require.Equal(t, "secret", cfg.RedisPassword)
		require.Equal(t, "secrets/gateway.crt", cfg.TLSCertPath)
		require.Equal(t, "secrets/gateway.key", cfg.TLSKeyPath)
		require.Equal(t, []string{"https://chat.example.com", "http://localhost:3000"}, cfg.CORSAllowedOrigins)
		require.Equal(t, int64(4096), cfg.MaxBodyBytes)
		require.Equal(t, 3*time.Second, cfg.RequestTimeout)
		require.Equal(t, uint32(600), cfg.RateLimitPerMinute)
		require.Equal(t, uint32(50), cfg.RateLimitBurst)
		require.Equal(t, "debug", cfg.LogLevel)
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
		"GATEWAY_MAX_BODY_BYTES":        {"0", "-1", "invalid", "4294967296"},
		"GATEWAY_REQUEST_TIMEOUT":       {"0s", "-1s", "soon"},
		"GATEWAY_RATE_LIMIT_PER_MINUTE": {"0", "-1", "many"},
		"GATEWAY_RATE_LIMIT_BURST":      {"0", "-1", "many"},
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
