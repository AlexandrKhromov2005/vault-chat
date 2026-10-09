package handler_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
)

func TestHealth(t *testing.T) {
	ok := func(context.Context) error { return nil }
	failing := func(context.Context) error { return errors.New("dial tcp 10.0.0.5:50051: connection refused") }

	t.Run("live", func(t *testing.T) {
		h := handler.NewHealth(map[string]handler.Check{"auth": failing}, slog.Default())

		rec := httptest.NewRecorder()
		h.Live(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

		// Liveness never depends on other services.
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
	})

	t.Run("ready", func(t *testing.T) {
		h := handler.NewHealth(map[string]handler.Check{"auth": ok}, slog.Default())

		rec := httptest.NewRecorder()
		h.Ready(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{"status":"ready","checks":{"auth":"ok"}}`, rec.Body.String())
	})

	t.Run("not ready", func(t *testing.T) {
		h := handler.NewHealth(map[string]handler.Check{"auth": failing, "other": ok}, slog.Default())

		rec := httptest.NewRecorder()
		h.Ready(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.JSONEq(t, `{"status":"not_ready","checks":{"auth":"fail","other":"ok"}}`, rec.Body.String())
	})

	t.Run("checks run with a deadline", func(t *testing.T) {
		var hasDeadline bool
		h := handler.NewHealth(map[string]handler.Check{"auth": func(ctx context.Context) error {
			_, hasDeadline = ctx.Deadline()
			return nil
		}}, slog.Default())

		h.Ready(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ready", nil))
		require.True(t, hasDeadline)
	})
}
