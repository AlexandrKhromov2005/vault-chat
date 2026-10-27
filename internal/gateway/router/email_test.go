package router_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/ratelimit"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/router"
)

var emailPostRoutes = []string{
	"/api/v1/auth/email/verification/request", "/api/v1/auth/email/verification/confirm",
	"/api/v1/auth/password/reset/request", "/api/v1/auth/password/reset/confirm",
	"/api/v1/auth/email/change/request", "/api/v1/auth/email/change/confirm",
}

func TestEmailRoutesCredentialQuotaFailsClosed(t *testing.T) {
	for _, path := range emailPostRoutes {
		t.Run(path, func(t *testing.T) {
			f := newFixture(t)
			f.limiter.EXPECT().Allow(mock.Anything, mock.Anything).Return(ratelimit.Result{Allowed: true}, nil)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			h := router.New(router.Deps{Auth: handler.NewAuthHandler(f.auth, logger), TokenValidator: f.validator,
				Limiter: f.limiter, AuthLimiter: unavailableCredentialLimiter{}, Logger: logger,
				Health: handler.NewHealth(nil, logger), RequestTimeout: time.Second, MaxBodyBytes: 128})
			r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
			r.RemoteAddr = "203.0.113.7:5555"
			r.Header.Set("Content-Type", "application/json")
			if path == "/api/v1/auth/email/change/request" {
				r.Header.Set("Authorization", "Bearer access-token")
				f.validator.EXPECT().ValidateToken(mock.Anything, &authv1.ValidateTokenRequest{Token: "access-token"}).
					Return(&authv1.ValidateTokenResponse{Valid: true, UserId: "u-1"}, nil)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			require.Equal(t, http.StatusServiceUnavailable, w.Code)
			f.auth.AssertNotCalled(t, "RequestEmailChange", mock.Anything, mock.Anything)
		})
	}
}

func TestEmailRoutesAlwaysConsumeTheLocalCredentialQuota(t *testing.T) {
	for _, path := range emailPostRoutes {
		t.Run(path, func(t *testing.T) {
			f := newFixture(t)
			f.limiter.EXPECT().Allow(mock.Anything, mock.Anything).Return(ratelimit.Result{Allowed: true}, nil)
			if path == "/api/v1/auth/email/change/request" {
				f.validator.EXPECT().ValidateToken(mock.Anything, &authv1.ValidateTokenRequest{Token: "access-token"}).
					Return(&authv1.ValidateTokenResponse{Valid: true, UserId: "u-1"}, nil)
			}
			for i := range 6 {
				r := httptest.NewRequest(http.MethodPost, path, strings.NewReader("!"))
				r.Header.Set("Content-Type", "application/json")
				if path == "/api/v1/auth/email/change/request" {
					r.Header.Set("Authorization", "Bearer access-token")
				}
				w := f.serve(r)
				want := http.StatusBadRequest
				if i == 5 {
					want = http.StatusTooManyRequests
				}
				require.Equal(t, want, w.Code)
			}
		})
	}
}

func TestEmailRoutesRequireAuthenticationOnlyForPrivateAccountOperations(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/auth/account"}, {http.MethodPost, "/api/v1/auth/email/change/request"},
	} {
		f := newFixture(t)
		f.allow("ip:203.0.113.7")
		r := httptest.NewRequest(tc.method, tc.path, nil)
		require.Equal(t, http.StatusUnauthorized, f.serve(r).Code)
	}
}

func TestEmailLinksCannotMutateThroughGET(t *testing.T) {
	for _, path := range emailPostRoutes {
		f := newFixture(t)
		f.allow("ip:203.0.113.7")
		w := f.serve(httptest.NewRequest(http.MethodGet, path+"?token=synthetic", nil))
		require.Equal(t, http.StatusNotFound, w.Code)
	}
}

func TestConcurrentEmailRequestsShareOneCredentialBudget(t *testing.T) {
	f := newFixture(t)
	f.limiter.EXPECT().Allow(mock.Anything, mock.Anything).Return(ratelimit.Result{Allowed: true}, nil)
	f.validator.EXPECT().ValidateToken(mock.Anything, &authv1.ValidateTokenRequest{Token: "access-token"}).
		Return(&authv1.ValidateTokenResponse{Valid: true, UserId: "u-1"}, nil)
	results := make(chan int, 30)
	var workers sync.WaitGroup
	for i := range 30 {
		workers.Go(func() {
			path := emailPostRoutes[i%len(emailPostRoutes)]
			r := httptest.NewRequest(http.MethodPost, path, strings.NewReader("!"))
			r.Header.Set("Content-Type", "application/json")
			if path == "/api/v1/auth/email/change/request" {
				r.Header.Set("Authorization", "Bearer access-token")
			}
			results <- f.serve(r).Code
		})
	}
	workers.Wait()
	close(results)
	counts := map[int]int{}
	for code := range results {
		counts[code]++
	}
	require.Equal(t, map[int]int{http.StatusBadRequest: 5, http.StatusTooManyRequests: 25}, counts)
}
