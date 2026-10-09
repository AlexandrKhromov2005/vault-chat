package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware/mocks"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/ratelimit"
)

func TestRateLimit(t *testing.T) {
	serve := func(limiter middleware.Limiter) (*httptest.ResponseRecorder, bool, string) {
		logger, buf := newLogger()
		called := false
		h := middleware.RateLimit(limiter, middleware.ByClientIP, logger)(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "203.0.113.7:5555"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec, called, buf.String()
	}

	t.Run("allowed request carries quota headers", func(t *testing.T) {
		limiter := mocks.NewMockLimiter(t)
		limiter.EXPECT().Allow(mock.Anything, "ip:203.0.113.7").
			Return(ratelimit.Result{Allowed: true, Limit: 30, Remaining: 29}, nil)

		rec, called, _ := serve(limiter)

		require.True(t, called)
		require.Equal(t, "30", rec.Header().Get("X-RateLimit-Limit"))
		require.Equal(t, "29", rec.Header().Get("X-RateLimit-Remaining"))
	})

	t.Run("exhausted quota is rejected", func(t *testing.T) {
		limiter := mocks.NewMockLimiter(t)
		limiter.EXPECT().Allow(mock.Anything, mock.Anything).
			Return(ratelimit.Result{Limit: 30, RetryAfter: 1500 * time.Millisecond}, nil)

		rec, called, _ := serve(limiter)

		require.False(t, called)
		require.Equal(t, http.StatusTooManyRequests, rec.Code)
		require.Equal(t, "2", rec.Header().Get("Retry-After"))
		require.Equal(t, "0", rec.Header().Get("X-RateLimit-Remaining"))
		require.JSONEq(t, `{"error":{"code":"rate_limited","message":"too many requests"}}`, rec.Body.String())
	})

	t.Run("Retry-After is at least one second", func(t *testing.T) {
		limiter := mocks.NewMockLimiter(t)
		limiter.EXPECT().Allow(mock.Anything, mock.Anything).
			Return(ratelimit.Result{Limit: 30}, nil)

		rec, _, _ := serve(limiter)
		require.Equal(t, "1", rec.Header().Get("Retry-After"))
	})

	t.Run("limiter failure fails open", func(t *testing.T) {
		limiter := mocks.NewMockLimiter(t)
		limiter.EXPECT().Allow(mock.Anything, mock.Anything).
			Return(ratelimit.Result{}, errors.New("redis: connection refused"))

		rec, called, logs := serve(limiter)

		require.True(t, called)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, logs, "rate limiter unavailable")
	})
}

func TestKeyFuncs(t *testing.T) {
	request := func(remoteAddr string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = remoteAddr
		return req
	}

	require.Equal(t, "ip:203.0.113.7", middleware.ByClientIP(request("203.0.113.7:5555")))
	require.Equal(t, "ip:::1", middleware.ByClientIP(request("[::1]:5555")))
	require.Equal(t, "ip:pipe", middleware.ByClientIP(request("pipe")))

	anonymous := request("203.0.113.7:5555")
	require.Equal(t, "ip:203.0.113.7", middleware.ByUserID(anonymous))

	authenticated := anonymous.WithContext(
		middleware.WithIdentity(context.Background(), middleware.Identity{UserID: "u-1"}))
	require.Equal(t, "user:u-1", middleware.ByUserID(authenticated))
}
