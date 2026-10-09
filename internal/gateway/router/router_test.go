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
	"google.golang.org/grpc"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	handlermocks "github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler/mocks"
	middlewaremocks "github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware/mocks"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/ratelimit"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/router"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/requestid"
)

type fixture struct {
	handler   http.Handler
	auth      *handlermocks.MockAuthClient
	validator *middlewaremocks.MockTokenValidator
	limiter   *middlewaremocks.MockLimiter
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	f := &fixture{
		auth:      handlermocks.NewMockAuthClient(t),
		validator: middlewaremocks.NewMockTokenValidator(t),
		limiter:   middlewaremocks.NewMockLimiter(t),
	}
	f.handler = router.New(router.Deps{
		Auth:               handler.NewAuthHandler(f.auth, logger),
		Health:             handler.NewHealth(map[string]handler.Check{"auth": func(context.Context) error { return nil }}, logger),
		TokenValidator:     f.validator,
		Limiter:            f.limiter,
		Logger:             logger,
		CORSAllowedOrigins: []string{"https://chat.example.com"},
		MaxBodyBytes:       64,
		RequestTimeout:     time.Second,
	})
	return f
}

func (f *fixture) allow(key string) {
	f.limiter.EXPECT().Allow(mock.Anything, key).
		Return(ratelimit.Result{Allowed: true, Limit: 30, Remaining: 29}, nil).Once()
}

func (f *fixture) serve(req *http.Request) *httptest.ResponseRecorder {
	req.RemoteAddr = "203.0.113.7:5555"
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func TestRouter_Probes(t *testing.T) {
	f := newFixture(t) // probes must not consume rate limit quota

	rec := f.serve(httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	rec = f.serve(httptest.NewRequest(http.MethodGet, "/ready", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestRouter_PublicAuthRoutes(t *testing.T) {
	t.Run("register", func(t *testing.T) {
		f := newFixture(t)
		f.allow("ip:203.0.113.7")
		f.auth.EXPECT().Register(mock.Anything, mock.Anything).
			Return(&authv1.RegisterResponse{UserId: "u-1"}, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		rec := f.serve(req)

		require.Equal(t, http.StatusCreated, rec.Code)
		require.Equal(t, "29", rec.Header().Get("X-RateLimit-Remaining"))
	})

	t.Run("login", func(t *testing.T) {
		f := newFixture(t)
		f.allow("ip:203.0.113.7")
		f.auth.EXPECT().Login(mock.Anything, mock.Anything).Return(&authv1.LoginResponse{UserId: "u-1"}, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		rec := f.serve(req)

		require.Equal(t, http.StatusOK, rec.Code)
	})
}

func TestRouter_SessionRoutes(t *testing.T) {
	t.Run("refresh is public", func(t *testing.T) {
		f := newFixture(t)
		f.allow("ip:203.0.113.7")
		f.auth.EXPECT().RefreshToken(mock.Anything, &authv1.RefreshTokenRequest{RefreshToken: "r"}).
			Return(&authv1.RefreshTokenResponse{UserId: "u-1"}, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(`{"refresh_token":"r"}`))
		req.Header.Set("Content-Type", "application/json")
		require.Equal(t, http.StatusOK, f.serve(req).Code)
	})

	t.Run("logout is public", func(t *testing.T) {
		f := newFixture(t)
		f.allow("ip:203.0.113.7")
		f.auth.EXPECT().Logout(mock.Anything, &authv1.LogoutRequest{RefreshToken: "r"}).
			Return(&authv1.LogoutResponse{}, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", strings.NewReader(`{"refresh_token":"r"}`))
		req.Header.Set("Content-Type", "application/json")
		require.Equal(t, http.StatusNoContent, f.serve(req).Code)
	})

	t.Run("revoking all sessions requires a token", func(t *testing.T) {
		f := newFixture(t)
		f.allow("ip:203.0.113.7")

		rec := f.serve(httptest.NewRequest(http.MethodDelete, "/api/v1/auth/sessions", nil))
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("revoking all sessions forwards the token", func(t *testing.T) {
		f := newFixture(t)
		f.allow("ip:203.0.113.7")
		f.allow("user:u-1")
		f.validator.EXPECT().ValidateToken(mock.Anything, &authv1.ValidateTokenRequest{Token: "tok"}).
			Return(&authv1.ValidateTokenResponse{Valid: true, UserId: "u-1"}, nil)
		f.auth.EXPECT().RevokeAllSessions(mock.Anything, &authv1.RevokeAllSessionsRequest{AccessToken: "tok"}).
			Return(&authv1.RevokeAllSessionsResponse{RevokedCount: 2}, nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/sessions", nil)
		req.Header.Set("Authorization", "Bearer tok")
		rec := f.serve(req)

		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{"revoked_count":2}`, rec.Body.String())
	})
}

func TestRouter_BackendCallsHaveDeadline(t *testing.T) {
	f := newFixture(t)
	f.allow("ip:203.0.113.7")
	f.auth.EXPECT().Login(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, _ *authv1.LoginRequest, _ ...grpc.CallOption) (*authv1.LoginResponse, error) {
			_, ok := ctx.Deadline()
			require.True(t, ok, "backend call must inherit the request deadline")
			require.NotEmpty(t, requestid.FromContext(ctx), "backend call must carry the request id")
			return &authv1.LoginResponse{}, nil
		})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	require.Equal(t, http.StatusOK, f.serve(req).Code)
}

func TestRouter_ProtectedRoute(t *testing.T) {
	t.Run("without token", func(t *testing.T) {
		f := newFixture(t)
		f.allow("ip:203.0.113.7")

		rec := f.serve(httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil))
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("with token is limited per user", func(t *testing.T) {
		f := newFixture(t)
		f.allow("ip:203.0.113.7")
		f.allow("user:u-1")
		f.validator.EXPECT().ValidateToken(mock.Anything, &authv1.ValidateTokenRequest{Token: "tok"}).
			Return(&authv1.ValidateTokenResponse{Valid: true, UserId: "u-1", Username: "username1"}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
		req.Header.Set("Authorization", "Bearer tok")
		rec := f.serve(req)

		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), `"username":"username1"`)
	})
}

func TestRouter_CrossCuttingConcerns(t *testing.T) {
	t.Run("every response has a request id and security headers", func(t *testing.T) {
		f := newFixture(t)

		rec := f.serve(httptest.NewRequest(http.MethodGet, "/health", nil))
		require.True(t, requestid.Valid(rec.Header().Get(requestid.Header)))
		require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	})

	t.Run("unknown routes return JSON 404", func(t *testing.T) {
		f := newFixture(t)
		f.allow("ip:203.0.113.7")

		for _, target := range []string{"/api/v1/nope", "/nope"} {
			rec := f.serve(httptest.NewRequest(http.MethodGet, target, nil))
			require.Equal(t, http.StatusNotFound, rec.Code, target)
			require.JSONEq(t, `{"error":{"code":"not_found","message":"route not found"}}`, rec.Body.String())
		}
	})

	t.Run("CORS preflight is answered before rate limiting", func(t *testing.T) {
		f := newFixture(t)

		req := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
		req.Header.Set("Origin", "https://chat.example.com")
		req.Header.Set("Access-Control-Request-Method", http.MethodPost)
		rec := f.serve(req)

		require.Equal(t, http.StatusNoContent, rec.Code)
		require.Equal(t, "https://chat.example.com", rec.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("oversized body is rejected", func(t *testing.T) {
		f := newFixture(t)
		f.allow("ip:203.0.113.7")

		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
			strings.NewReader(`{"email":"`+strings.Repeat("a", 100)+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := f.serve(req)

		require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	})

	t.Run("rate limited client", func(t *testing.T) {
		f := newFixture(t)
		f.limiter.EXPECT().Allow(mock.Anything, "ip:203.0.113.7").
			Return(ratelimit.Result{Limit: 30, RetryAfter: time.Second}, nil)

		rec := f.serve(httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil))
		require.Equal(t, http.StatusTooManyRequests, rec.Code)
	})
}
