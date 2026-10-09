package router_test

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	handlermocks "github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler/mocks"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
	middlewaremocks "github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware/mocks"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/ratelimit"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/router"
)

const (
	// budget is GATEWAY_REQUEST_TIMEOUT for these tests.
	budget = 50 * time.Millisecond
	// slowness is how long the slow dependencies take. It is far beyond the
	// budget, so a request that waits for it is easy to tell apart from one
	// cut off at the deadline, even on a slow machine under -race.
	slowness = 2 * time.Second
	// cutoff is the latest a request may finish when the budget is enforced.
	cutoff = 500 * time.Millisecond
)

// delayedLimiter allows every request after delay, or fails when the request
// context ends first, as the Redis limiter does.
type delayedLimiter struct{ delay time.Duration }

func (l delayedLimiter) Allow(ctx context.Context, _ string) (ratelimit.Result, error) {
	select {
	case <-time.After(l.delay):
		return ratelimit.Result{Allowed: true, Limit: 30, Remaining: 29}, nil
	case <-ctx.Done():
		return ratelimit.Result{}, ctx.Err()
	}
}

func newBudgetRouter(t *testing.T, limiter middleware.Limiter) (http.Handler, *handlermocks.MockAuthClient) {
	t.Helper()
	auth := handlermocks.NewMockAuthClient(t)
	return newBudgetRouterFor(t, limiter, auth, middlewaremocks.NewMockTokenValidator(t)), auth
}

func newBudgetRouterWith(t *testing.T, limiter middleware.Limiter, validator middleware.TokenValidator) http.Handler {
	t.Helper()
	return newBudgetRouterFor(t, limiter, handlermocks.NewMockAuthClient(t), validator)
}

func newBudgetRouterFor(t *testing.T, limiter middleware.Limiter, auth handler.AuthClient,
	validator middleware.TokenValidator) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return router.New(router.Deps{
		Auth:           handler.NewAuthHandler(auth, logger),
		Health:         handler.NewHealth(nil, logger),
		TokenValidator: validator,
		Limiter:        limiter,
		Logger:         logger,
		MaxBodyBytes:   1 << 20,
		RequestTimeout: budget,
	})
}

func TestRouter_RequestTimeout(t *testing.T) {
	t.Run("request within the budget succeeds", func(t *testing.T) {
		h, auth := newBudgetRouter(t, delayedLimiter{})
		auth.EXPECT().Login(mock.Anything, mock.Anything).Return(&authv1.LoginResponse{UserId: "u-1"}, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		start := time.Now()
		h.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		require.Less(t, time.Since(start), cutoff)
	})

	t.Run("slow rate limiter is cut off at the deadline", func(t *testing.T) {
		h, _ := newBudgetRouter(t, delayedLimiter{delay: slowness}) // auth must not be called

		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		start := time.Now()
		h.ServeHTTP(rec, req)

		require.Less(t, time.Since(start), cutoff)
		require.Equal(t, http.StatusGatewayTimeout, rec.Code)
		require.JSONEq(t, `{"error":{"code":"timeout","message":"request timed out"}}`, rec.Body.String())
	})

	t.Run("slow backend is cut off at the deadline with 504", func(t *testing.T) {
		// A real server matters here: for requests without a body net/http
		// reads the connection in the background, and that read sees the
		// connection deadline too.
		validator := middlewaremocks.NewMockTokenValidator(t)
		validator.EXPECT().ValidateToken(mock.Anything, mock.Anything).RunAndReturn(
			func(ctx context.Context, _ *authv1.ValidateTokenRequest, _ ...grpc.CallOption) (*authv1.ValidateTokenResponse, error) {
				// Like a gRPC call: wait for the context, report its error.
				<-ctx.Done()
				return nil, status.FromContextError(ctx.Err()).Err()
			})
		server := httptest.NewServer(newBudgetRouterWith(t, delayedLimiter{}, validator))
		t.Cleanup(server.Close)

		req, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/auth/me", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer tok")
		start := time.Now()
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		elapsed := time.Since(start)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Less(t, elapsed, cutoff)
		require.Equal(t, http.StatusGatewayTimeout, resp.StatusCode, string(body))
		require.Contains(t, string(body), `"code":"timeout"`)
	})

	t.Run("slow backend after a fully read body is cut off with 504", func(t *testing.T) {
		auth := handlermocks.NewMockAuthClient(t)
		auth.EXPECT().Login(mock.Anything, mock.Anything).RunAndReturn(
			func(ctx context.Context, _ *authv1.LoginRequest, _ ...grpc.CallOption) (*authv1.LoginResponse, error) {
				<-ctx.Done()
				return nil, status.FromContextError(ctx.Err()).Err()
			})
		server := httptest.NewServer(newBudgetRouterFor(t, delayedLimiter{}, auth, middlewaremocks.NewMockTokenValidator(t)))
		t.Cleanup(server.Close)

		start := time.Now()
		resp, err := http.Post(server.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{}`))
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		elapsed := time.Since(start)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Less(t, elapsed, cutoff)
		require.Equal(t, http.StatusGatewayTimeout, resp.StatusCode, string(body))
		require.Contains(t, string(body), `"code":"timeout"`)
	})

	t.Run("slow request body is cut off at the deadline", func(t *testing.T) {
		h, _ := newBudgetRouter(t, delayedLimiter{}) // auth must not be called
		server := httptest.NewServer(h)
		t.Cleanup(server.Close)

		conn, err := net.Dial("tcp", server.Listener.Addr().String())
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		require.NoError(t, conn.SetDeadline(time.Now().Add(10*time.Second)))

		// Send the headers and the first byte of a two-byte body, then stall.
		_, err = io.WriteString(conn, "POST /api/v1/auth/login HTTP/1.1\r\n"+
			"Host: gateway\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n[")
		require.NoError(t, err)
		start := time.Now()
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		elapsed := time.Since(start)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Less(t, elapsed, cutoff)
		require.Equal(t, http.StatusRequestTimeout, resp.StatusCode, string(body))
		require.Contains(t, string(body), `"code":"request_timeout"`)
	})
}
