package logger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/logger"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/requestid"
)

func newBufferLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(logger.NewRequestIDHandler(slog.NewJSONHandler(&buf, nil))), &buf
}

func decodeLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var entry map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	return entry
}

func TestRequestIDHandler(t *testing.T) {
	withID := requestid.NewContext(context.Background(), "req-1")

	t.Run("adds the request id from the context", func(t *testing.T) {
		l, buf := newBufferLogger()
		l.InfoContext(withID, "user logged in", "user_id", "u-1")

		entry := decodeLine(t, buf)
		require.Equal(t, "req-1", entry["request_id"])
		require.Equal(t, "u-1", entry["user_id"])
	})

	t.Run("no request id in the context", func(t *testing.T) {
		l, buf := newBufferLogger()
		l.InfoContext(context.Background(), "started")

		require.NotContains(t, decodeLine(t, buf), "request_id")
	})

	t.Run("an explicit request_id is not duplicated", func(t *testing.T) {
		l, buf := newBufferLogger()
		l.InfoContext(withID, "http request", "request_id", "req-1")

		require.Equal(t, 1, strings.Count(buf.String(), `"request_id"`))
	})

	t.Run("survives With", func(t *testing.T) {
		l, buf := newBufferLogger()
		l.With("service", "auth").InfoContext(withID, "user registered")

		entry := decodeLine(t, buf)
		require.Equal(t, "req-1", entry["request_id"])
		require.Equal(t, "auth", entry["service"])
	})

	t.Run("respects the level of the wrapped handler", func(t *testing.T) {
		var buf bytes.Buffer
		l := slog.New(logger.NewRequestIDHandler(
			slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
		l.InfoContext(withID, "dropped")

		require.Empty(t, buf.String())
	})
}
