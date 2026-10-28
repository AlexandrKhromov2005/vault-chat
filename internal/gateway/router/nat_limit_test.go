package router_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	handlermocks "github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler/mocks"
	middlewaremocks "github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware/mocks"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/router"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/ratelimit"
)

// natAddr is the one address many users share behind a carrier NAT.
const natAddr = "198.51.100.20:4000"

type natFixture struct {
	handler   http.Handler
	auth      *handlermocks.MockAuthClient
	validator *middlewaremocks.MockTokenValidator
}

// newNATFixture uses real process-local buckets with the default settings
// so that quotas are exercised, not mocked.
func newNATFixture(t *testing.T, mutate ...func(*router.Deps)) *natFixture {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := &natFixture{auth: handlermocks.NewMockAuthClient(t), validator: middlewaremocks.NewMockTokenValidator(t)}
	deps := router.Deps{
		Auth:           handler.NewAuthHandler(f.auth, logger),
		Health:         handler.NewHealth(nil, logger),
		TokenValidator: f.validator,
		// Burst 30 as by default; refilling once a minute keeps slow test
		// runs from regaining tokens.
		Limiter:        ratelimit.NewMemoryLimiter(time.Minute, 30, 4096),
		Logger:         logger,
		MaxBodyBytes:   1 << 10,
		RequestTimeout: time.Second,
	}
	for _, m := range mutate {
		m(&deps)
	}
	f.handler = router.New(deps)
	return f
}

func (f *natFixture) post(path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.RemoteAddr = natAddr
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func loginBody(i int) string {
	return fmt.Sprintf(`{"email":"user%d@example.test","password":"synthetic-password"}`, i)
}

func TestUsersBehindOneAddressCanAllLogIn(t *testing.T) {
	f := newNATFixture(t)
	f.auth.EXPECT().Login(mock.Anything, mock.Anything).Return(&authv1.LoginResponse{UserId: "u"}, nil)
	for i := range 20 {
		require.Equal(t, http.StatusOK, f.post("/api/v1/auth/login", loginBody(i)).Code, "login %d", i)
	}
}

func TestUsersBehindOneAddressCanAllRegister(t *testing.T) {
	f := newNATFixture(t)
	f.auth.EXPECT().Register(mock.Anything, mock.Anything).Return(&authv1.RegisterResponse{UserId: "u"}, nil)
	for i := range 20 {
		body := fmt.Sprintf(`{"email":"user%d@example.test","username":"user%d","password":"p","invitation_token":"t"}`, i, i)
		require.Equal(t, http.StatusCreated, f.post("/api/v1/auth/register", body).Code, "registration %d", i)
	}
}

func TestAuthenticatedUsersBehindOneAddressAreLimitedPerUser(t *testing.T) {
	f := newNATFixture(t)
	f.validator.EXPECT().ValidateToken(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, in *authv1.ValidateTokenRequest, _ ...grpc.CallOption) (*authv1.ValidateTokenResponse, error) {
			if !strings.HasPrefix(in.GetToken(), "valid-") {
				return &authv1.ValidateTokenResponse{}, nil
			}
			return &authv1.ValidateTokenResponse{Valid: true, UserId: strings.TrimPrefix(in.GetToken(), "valid-")}, nil
		})
	get := func(token string) int {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
		r.RemoteAddr = natAddr
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		return w.Code
	}
	// Forty users with five requests each exceed the address burst of 30
	// several times over; each stays far below its own quota.
	for i := range 200 {
		require.Equal(t, http.StatusOK, get(fmt.Sprintf("valid-u%d", i%40)), "request %d", i)
	}
	// Requests that fail authentication keep their address token.
	codes := map[int]int{}
	for range 40 {
		codes[get("forged")]++
	}
	require.Equal(t, map[int]int{http.StatusUnauthorized: 30, http.StatusTooManyRequests: 10}, codes)
}

// wrongPassword is how Auth answers a failed login.
var wrongPassword = status.Error(codes.Unauthenticated, "invalid credentials")

func TestGuessingOneAccountStaysAtFiveAttemptsPerAddress(t *testing.T) {
	f := newNATFixture(t)
	f.auth.EXPECT().Login(mock.Anything, mock.Anything).Return(nil, wrongPassword)
	for i := range 5 {
		require.Equal(t, http.StatusUnauthorized, f.post("/api/v1/auth/login", loginBody(0)).Code, "attempt %d", i)
	}
	// Changing the case of the email does not open a new bucket.
	w := f.post("/api/v1/auth/login", `{"email":"USER0@example.test","password":"guess"}`)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.NotEmpty(t, w.Header().Get("Retry-After"))
	require.Equal(t, http.StatusUnauthorized, f.post("/api/v1/auth/login", loginBody(1)).Code, "other accounts are unaffected")
}

func TestFailedLoginsFromOneAddressAreLimitedAcrossAccounts(t *testing.T) {
	f := newNATFixture(t)
	f.auth.EXPECT().Login(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, in *authv1.LoginRequest, _ ...grpc.CallOption) (*authv1.LoginResponse, error) {
			if in.GetPassword() == "correct" {
				return &authv1.LoginResponse{UserId: "u"}, nil
			}
			return nil, wrongPassword
		})
	for i := range 10 {
		// Logging into own accounts between guesses returns only the
		// tokens of those logins.
		known := fmt.Sprintf(`{"email":"own%d@example.test","password":"correct"}`, i)
		require.Equal(t, http.StatusOK, f.post("/api/v1/auth/login", known).Code)
		require.Equal(t, http.StatusUnauthorized, f.post("/api/v1/auth/login", loginBody(i)).Code, "guess %d", i)
	}
	require.Equal(t, http.StatusTooManyRequests, f.post("/api/v1/auth/login", loginBody(10)).Code)
	// Tokens are taken before the outcome is known, so the address now
	// waits for a refill even for a correct password.
	require.Equal(t, http.StatusTooManyRequests, f.post("/api/v1/auth/login", `{"email":"own0@example.test","password":"correct"}`).Code)
}

func TestConcurrentFailedLoginsCannotOutrunTheAddressQuota(t *testing.T) {
	f := newNATFixture(t)
	var reached atomic.Int32
	f.auth.EXPECT().Login(mock.Anything, mock.Anything).RunAndReturn(
		func(context.Context, *authv1.LoginRequest, ...grpc.CallOption) (*authv1.LoginResponse, error) {
			reached.Add(1)
			time.Sleep(10 * time.Millisecond)
			return nil, wrongPassword
		})
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() { f.post("/api/v1/auth/login", loginBody(i)) })
	}
	wg.Wait()
	require.Equal(t, int32(10), reached.Load())
}

func TestRefreshBehindOneAddress(t *testing.T) {
	f := newNATFixture(t, func(d *router.Deps) { d.RefreshLimiter = unavailableCredentialLimiter{} })
	f.auth.EXPECT().RefreshToken(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, in *authv1.RefreshTokenRequest, _ ...grpc.CallOption) (*authv1.RefreshTokenResponse, error) {
			if in.GetRefreshToken() == "valid" {
				return &authv1.RefreshTokenResponse{UserId: "u"}, nil
			}
			return nil, status.Error(codes.Unauthenticated, "invalid refresh token")
		})
	// Without Redis, successful refreshes are neither refused nor limited
	// by the address quota.
	for i := range 60 {
		require.Equal(t, http.StatusOK, f.post("/api/v1/auth/refresh", `{"refresh_token":"valid"}`).Code, "refresh %d", i)
	}
	for i := range 30 {
		require.Equal(t, http.StatusUnauthorized, f.post("/api/v1/auth/refresh", `{"refresh_token":"forged"}`).Code, "forgery %d", i)
	}
	require.Equal(t, http.StatusTooManyRequests, f.post("/api/v1/auth/refresh", `{"refresh_token":"forged"}`).Code,
		"the local bucket holds while Redis is down")
}

func TestCredentialAddressQuotaFailsClosed(t *testing.T) {
	f := newNATFixture(t, func(d *router.Deps) { d.AuthIPLimiter = unavailableCredentialLimiter{} })
	require.Equal(t, http.StatusServiceUnavailable, f.post("/api/v1/auth/login", loginBody(0)).Code)
	require.Equal(t, http.StatusServiceUnavailable, f.post("/api/v1/auth/register", `{}`).Code)
}

func TestCredentialQuotasAreConfigurable(t *testing.T) {
	f := newNATFixture(t, func(d *router.Deps) {
		d.AuthIPRateLimitPerMinute, d.AuthIPRateLimitBurst = 1, 2
		d.RefreshRateLimitPerMinute, d.RefreshRateLimitBurst = 1, 1
	})
	f.auth.EXPECT().Login(mock.Anything, mock.Anything).Return(nil, wrongPassword)
	f.auth.EXPECT().RefreshToken(mock.Anything, mock.Anything).Return(nil, status.Error(codes.Unauthenticated, "invalid"))
	require.Equal(t, http.StatusUnauthorized, f.post("/api/v1/auth/login", loginBody(0)).Code)
	require.Equal(t, http.StatusUnauthorized, f.post("/api/v1/auth/login", loginBody(1)).Code)
	require.Equal(t, http.StatusTooManyRequests, f.post("/api/v1/auth/login", loginBody(2)).Code)
	require.Equal(t, http.StatusUnauthorized, f.post("/api/v1/auth/refresh", `{"refresh_token":"r"}`).Code)
	require.Equal(t, http.StatusTooManyRequests, f.post("/api/v1/auth/refresh", `{"refresh_token":"r"}`).Code)
}
