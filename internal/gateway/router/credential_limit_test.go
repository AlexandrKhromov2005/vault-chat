package router_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/ratelimit"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/router"
)

func TestCredentialRoutesShareLocalQuota(t *testing.T) {
	f := newFixture(t)
	// Even a Redis limiter that grants every request cannot bypass the
	// process-local guard by losing its keys.
	f.limiter.EXPECT().Allow(mock.Anything, mock.Anything).
		Return(ratelimit.Result{Allowed: true}, nil)
	for i := range 6 {
		path := "/api/v1/auth/login"
		if i%2 == 1 {
			path = "/api/v1/auth/register"
		}
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader("!"))
		r.Header.Set("Content-Type", "application/json")
		w := f.serve(r)
		want := http.StatusBadRequest
		if i == 5 {
			want = http.StatusTooManyRequests
			require.NotEmpty(t, w.Header().Get("Retry-After"))
		}
		require.Equal(t, want, w.Code)
	}
}

type unavailableCredentialLimiter struct{}

func (unavailableCredentialLimiter) Allow(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{}, io.EOF
}

func TestCredentialRoutesRejectRedisOutage(t *testing.T) {
	f := newFixture(t)
	f.limiter.EXPECT().Allow(mock.Anything, mock.Anything).
		Return(ratelimit.Result{Allowed: true}, nil)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := router.New(router.Deps{
		Auth: handler.NewAuthHandler(f.auth, logger), Limiter: f.limiter,
		AuthLimiter: unavailableCredentialLimiter{}, Logger: logger,
		Health: handler.NewHealth(nil, logger), RequestTimeout: time.Second, MaxBodyBytes: 64,
	})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}
