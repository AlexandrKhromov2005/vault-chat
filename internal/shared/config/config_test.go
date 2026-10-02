package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/config"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AUTH_DATABASE_URL", "postgres://auth:auth@localhost:5432/auth_db")
	t.Setenv("AUTH_JWT_PRIVATE_KEY_PATH", "secrets/private.pem")
	t.Setenv("AUTH_JWT_PUBLIC_KEY_PATH", "secrets/public.pem")
}

func TestLoad(t *testing.T) {
	t.Run("defaults with required variables set", func(t *testing.T) {
		setRequiredEnv(t)

		cfg, err := config.Load()
		require.NoError(t, err)
		require.Equal(t, ":50051", cfg.GRPCAddr)
		require.Equal(t, "postgres://auth:auth@localhost:5432/auth_db", cfg.DatabaseURL)
		require.Equal(t, 15*time.Minute, cfg.JWTAccessTTL)
		require.Equal(t, 720*time.Hour, cfg.JWTRefreshTTL)
		require.Equal(t, uint32(65536), cfg.Argon2MemoryKiB)
		require.Equal(t, uint32(3), cfg.Argon2Time)
		require.Equal(t, uint8(2), cfg.Argon2Parallelism)
		require.Equal(t, "info", cfg.LogLevel)
	})

	t.Run("explicit values win", func(t *testing.T) {
		setRequiredEnv(t)
		t.Setenv("AUTH_GRPC_ADDR", ":6001")
		t.Setenv("AUTH_JWT_ACCESS_TTL", "5m")
		t.Setenv("AUTH_ARGON2_TIME", "4")
		t.Setenv("AUTH_LOG_LEVEL", "debug")

		cfg, err := config.Load()
		require.NoError(t, err)
		require.Equal(t, ":6001", cfg.GRPCAddr)
		require.Equal(t, 5*time.Minute, cfg.JWTAccessTTL)
		require.Equal(t, uint32(4), cfg.Argon2Time)
		require.Equal(t, "debug", cfg.LogLevel)
	})

	t.Run("missing required variables", func(t *testing.T) {
		for _, key := range []string{
			"AUTH_DATABASE_URL",
			"AUTH_JWT_PRIVATE_KEY_PATH",
			"AUTH_JWT_PUBLIC_KEY_PATH",
		} {
			t.Run(key, func(t *testing.T) {
				setRequiredEnv(t)
				t.Setenv(key, "")

				_, err := config.Load()
				require.ErrorContains(t, err, key)
			})
		}
	})

	t.Run("invalid duration", func(t *testing.T) {
		setRequiredEnv(t)
		t.Setenv("AUTH_JWT_ACCESS_TTL", "not-a-duration")

		_, err := config.Load()
		require.ErrorContains(t, err, "AUTH_JWT_ACCESS_TTL")
	})

	t.Run("invalid integer", func(t *testing.T) {
		setRequiredEnv(t)
		t.Setenv("AUTH_ARGON2_TIME", "-1")

		_, err := config.Load()
		require.ErrorContains(t, err, "AUTH_ARGON2_TIME")
	})
}
