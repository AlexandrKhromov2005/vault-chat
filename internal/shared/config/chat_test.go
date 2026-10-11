package config_test

import (
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/config"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestLoadChat(t *testing.T) {
	for _, key := range []string{"CHAT_GRPC_ADDR", "CHAT_AUTH_GRPC_ADDR", "CHAT_DATABASE_URL", "CHAT_REQUEST_TIMEOUT", "CHAT_LOG_LEVEL"} {
		t.Setenv(key, "")
	}
	_, err := config.LoadChat()
	require.Error(t, err)
	t.Setenv("CHAT_DATABASE_URL", "postgres://chat-db")
	cfg, err := config.LoadChat()
	require.NoError(t, err)
	require.Equal(t, ":50052", cfg.GRPCAddr)
	require.Equal(t, "localhost:50051", cfg.AuthGRPCAddr)
	require.Equal(t, 10*time.Second, cfg.RequestTimeout)
	require.Equal(t, "info", cfg.LogLevel)
	t.Setenv("CHAT_GRPC_ADDR", "127.0.0.1:50152")
	t.Setenv("CHAT_AUTH_GRPC_ADDR", "auth:50051")
	t.Setenv("CHAT_LOG_LEVEL", "debug")
	t.Setenv("CHAT_REQUEST_TIMEOUT", "2s")
	cfg, err = config.LoadChat()
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:50152", cfg.GRPCAddr)
	require.Equal(t, "auth:50051", cfg.AuthGRPCAddr)
	require.Equal(t, "debug", cfg.LogLevel)
	require.Equal(t, 2*time.Second, cfg.RequestTimeout)
	for _, value := range []string{"bad", "0s", "-1s"} {
		t.Setenv("CHAT_REQUEST_TIMEOUT", value)
		_, err := config.LoadChat()
		require.Error(t, err)
	}
}
