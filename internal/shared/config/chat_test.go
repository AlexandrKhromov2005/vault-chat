package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/config"
)

func TestLoadChat(t *testing.T) {
	for _, key := range []string{"CHAT_GRPC_ADDR", "CHAT_AUTH_GRPC_ADDR", "CHAT_DATABASE_URL", "CHAT_REQUEST_TIMEOUT", "CHAT_LOG_LEVEL", "CHAT_REDIS_ADDR", "CHAT_REDIS_PASSWORD",
		"CHAT_EVENT_QUEUE_SIZE", "CHAT_EVENT_PUBLISH_TIMEOUT", "CHAT_EVENT_DRAIN_TIMEOUT"} {
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
	require.Equal(t, "localhost:6379", cfg.RedisAddr)
	require.Empty(t, cfg.RedisPassword)
	t.Setenv("CHAT_GRPC_ADDR", "127.0.0.1:50152")
	t.Setenv("CHAT_REDIS_ADDR", "redis:6379")
	t.Setenv("CHAT_REDIS_PASSWORD", "secret")
	t.Setenv("CHAT_AUTH_GRPC_ADDR", "auth:50051")
	t.Setenv("CHAT_LOG_LEVEL", "debug")
	t.Setenv("CHAT_REQUEST_TIMEOUT", "2s")
	cfg, err = config.LoadChat()
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:50152", cfg.GRPCAddr)
	require.Equal(t, "auth:50051", cfg.AuthGRPCAddr)
	require.Equal(t, "debug", cfg.LogLevel)
	require.Equal(t, 2*time.Second, cfg.RequestTimeout)
	require.Equal(t, "redis:6379", cfg.RedisAddr)
	require.Equal(t, "secret", cfg.RedisPassword)
	for _, value := range []string{"bad", "0s", "-1s"} {
		t.Setenv("CHAT_REQUEST_TIMEOUT", value)
		_, err := config.LoadChat()
		require.Error(t, err)
	}
}

func TestLoadChat_EventQueue(t *testing.T) {
	t.Setenv("CHAT_DATABASE_URL", "postgres://chat-db")
	for _, key := range []string{"CHAT_EVENT_QUEUE_SIZE", "CHAT_EVENT_PUBLISH_TIMEOUT", "CHAT_EVENT_DRAIN_TIMEOUT"} {
		t.Setenv(key, "")
	}
	cfg, err := config.LoadChat()
	require.NoError(t, err)
	require.Equal(t, 1024, cfg.EventQueueSize)
	require.Equal(t, time.Second, cfg.EventPublishTimeout)
	require.Equal(t, 5*time.Second, cfg.EventDrainTimeout)
	t.Setenv("CHAT_EVENT_QUEUE_SIZE", "64")
	t.Setenv("CHAT_EVENT_PUBLISH_TIMEOUT", "300ms")
	t.Setenv("CHAT_EVENT_DRAIN_TIMEOUT", "2s")
	cfg, err = config.LoadChat()
	require.NoError(t, err)
	require.Equal(t, 64, cfg.EventQueueSize)
	require.Equal(t, 300*time.Millisecond, cfg.EventPublishTimeout)
	require.Equal(t, 2*time.Second, cfg.EventDrainTimeout)
	for key, values := range map[string][]string{
		"CHAT_EVENT_QUEUE_SIZE":      {"0", "-1", "x", "1048577"},
		"CHAT_EVENT_PUBLISH_TIMEOUT": {"0s", "bad"},
		"CHAT_EVENT_DRAIN_TIMEOUT":   {"-1s", "bad"},
	} {
		for _, value := range values {
			t.Run(key+"="+value, func(t *testing.T) {
				t.Setenv(key, value)
				_, err := config.LoadChat()
				require.Error(t, err)
			})
		}
	}
}

func FuzzChatRequestTimeout(f *testing.F) {
	for _, input := range []string{"", "10s", "0s", "bad", "-1s", "1ns"} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if strings.ContainsRune(input, 0) {
			t.Skip("environment values cannot contain NUL")
		}
		t.Setenv("CHAT_DATABASE_URL", "postgres://test")
		t.Setenv("CHAT_REQUEST_TIMEOUT", input)
		cfg, err := config.LoadChat()
		parsed, parseErr := time.ParseDuration(input)
		if input == "" {
			require.NoError(t, err)
			require.Equal(t, 10*time.Second, cfg.RequestTimeout)
			return
		}
		if parseErr != nil || parsed <= 0 {
			require.Error(t, err)
			return
		}
		require.NoError(t, err)
		require.Equal(t, parsed, cfg.RequestTimeout)
	})
}
