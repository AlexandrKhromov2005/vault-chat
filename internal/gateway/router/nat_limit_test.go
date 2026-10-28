package router_test

import (
	"context"
	"fmt"
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
