package router_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	handlermocks "github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler/mocks"
	middlewaremocks "github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware/mocks"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/profilecache"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/ratelimit"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/router"
)

const peer = "6f1c2a3b-4d5e-4f60-8a7b-9c0d1e2f3a4b"

type cacheFixture struct {
	handler   http.Handler
	auth      *handlermocks.MockAuthClient
	validator *middlewaremocks.MockTokenValidator
	cache     *profilecache.Cache
	redis     *miniredis.Miniredis
}

// newCacheFixture is a router whose profile route uses a cache on its own
// miniredis, or none when cached is false. Rate limits always allow.
func newCacheFixture(t *testing.T, cached bool) *cacheFixture {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	limiter := middlewaremocks.NewMockLimiter(t)
	limiter.EXPECT().Allow(mock.Anything, mock.Anything).Return(ratelimit.Result{Allowed: true, Limit: 30, Remaining: 29}, nil).Maybe()
	f := &cacheFixture{auth: handlermocks.NewMockAuthClient(t), validator: middlewaremocks.NewMockTokenValidator(t), redis: miniredis.RunT(t)}
	auth := handler.NewAuthHandler(f.auth, logger)
	if cached {
		client := ratelimit.NewRedisClient(f.redis.Addr(), "")
		t.Cleanup(func() { _ = client.Close() })
		f.cache = profilecache.New(client)
		auth.WithProfileCache(f.cache)
	}
	f.handler = router.New(router.Deps{
		Auth:           auth,
		Chat:           handler.NewChatHandler(handlermocks.NewMockChatClient(t), logger),
		Health:         handler.NewHealth(map[string]handler.Check{"auth": func(context.Context) error { return nil }}, logger),
		TokenValidator: f.validator,
		Limiter:        limiter,
		Logger:         logger,
		MaxBodyBytes:   64,
		RequestTimeout: time.Second,
	})
	return f
}

func (f *cacheFixture) session(times int) {
	f.validator.EXPECT().ValidateToken(mock.Anything, &authv1.ValidateTokenRequest{Token: "tok"}).
		Return(&authv1.ValidateTokenResponse{Valid: true, UserId: "u-1"}, nil).Times(times)
}

func (f *cacheFixture) get(token, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+id, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.RemoteAddr = "203.0.113.7:5555"
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

// Scenario 1: the first lookup calls GetUser, the second is served from the
// cache with the same body. The mock fails the test on a second GetUser.
func TestProfileCache_SecondLookupSkipsGetUser(t *testing.T) {
	f := newCacheFixture(t, true)
	f.session(2)
	f.auth.EXPECT().GetUser(mock.Anything, &authv1.GetUserRequest{AccessToken: "tok", UserId: peer}).
		Return(&authv1.GetUserResponse{UserId: peer, Username: "bob"}, nil).Once()
	first := f.get("tok", peer)
	second := f.get("tok", peer)
	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, http.StatusOK, second.Code)
	require.JSONEq(t, `{"user_id":"`+peer+`","username":"bob"}`, second.Body.String())
	require.Equal(t, first.Body.String(), second.Body.String())
	hits, misses, errs := f.cache.Counts()
	require.Equal(t, [3]uint64{1, 1, 0}, [3]uint64{hits, misses, errs})
}

// Scenarios 3 and 8: with the profile in the cache, a request without a
// token, with a revoked session or while Auth is down gets exactly what it
// gets without a cache, and the cache is not read.
func TestProfileCache_SessionIsCheckedFirst(t *testing.T) {
	cases := map[string]struct {
		token string
		err   error
	}{
		"no token":        {token: ""},
		"revoked session": {token: "revoked", err: status.Error(codes.Unauthenticated, "session revoked")},
		"auth down":       {token: "tok", err: status.Error(codes.Unavailable, "connection refused")},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			responses := make([]*httptest.ResponseRecorder, 0, 2)
			for _, cached := range []bool{false, true} {
				f := newCacheFixture(t, cached)
				if cached {
					f.cache.Put(context.Background(), profilecache.Profile{UserID: peer, Username: "bob"})
				}
				if c.token != "" {
					f.validator.EXPECT().ValidateToken(mock.Anything, &authv1.ValidateTokenRequest{Token: c.token}).Return(nil, c.err).Once()
				}
				responses = append(responses, f.get(c.token, peer))
				if cached {
					hits, misses, errs := f.cache.Counts()
					require.Zero(t, hits+misses+errs, "the cache was read")
				}
			}
			require.NotEqual(t, http.StatusOK, responses[1].Code)
			require.Equal(t, responses[0].Code, responses[1].Code)
			require.Equal(t, responses[0].Body.String(), responses[1].Body.String())
		})
	}
}

// Scenario 5: Auth's NotFound is remembered for a minute and answered with
// the same body; afterwards the lookup goes to Auth again.
func TestProfileCache_MissingAccountForOneMinute(t *testing.T) {
	f := newCacheFixture(t, true)
	f.session(3)
	notFound := status.Error(codes.NotFound, "user not found")
	f.auth.EXPECT().GetUser(mock.Anything, &authv1.GetUserRequest{AccessToken: "tok", UserId: peer}).Return(nil, notFound).Twice()
	first := f.get("tok", peer)
	second := f.get("tok", peer)
	require.Equal(t, http.StatusNotFound, first.Code)
	require.Equal(t, first.Code, second.Code)
	require.Equal(t, first.Body.String(), second.Body.String())
	f.redis.FastForward(profilecache.MissingTTL)
	require.Equal(t, http.StatusNotFound, f.get("tok", peer).Code)
}

// Other spellings of an id, and errors other than NotFound, never reach the
// cache, so one account has one key and failures are not remembered.
func TestProfileCache_OnlyCanonicalIDsAndRealAnswers(t *testing.T) {
	f := newCacheFixture(t, true)
	upper := "6F1C2A3B-4D5E-4F60-8A7B-9C0D1E2F3A4B"
	f.session(4)
	f.auth.EXPECT().GetUser(mock.Anything, &authv1.GetUserRequest{AccessToken: "tok", UserId: upper}).
		Return(&authv1.GetUserResponse{UserId: peer, Username: "bob"}, nil).Twice()
	f.auth.EXPECT().GetUser(mock.Anything, &authv1.GetUserRequest{AccessToken: "tok", UserId: peer}).
		Return(nil, status.Error(codes.Internal, "database down")).Twice()
	for range 2 {
		require.Equal(t, http.StatusOK, f.get("tok", upper).Code)
		require.Equal(t, http.StatusInternalServerError, f.get("tok", peer).Code)
	}
	require.Empty(t, f.redis.Keys())
}

// Scenario 6: without the cache instance the profile still comes from Auth,
// and the failure is counted.
func TestProfileCache_UnreachableCacheFallsBackToAuth(t *testing.T) {
	f := newCacheFixture(t, true)
	f.redis.Close()
	f.session(1)
	f.auth.EXPECT().GetUser(mock.Anything, &authv1.GetUserRequest{AccessToken: "tok", UserId: peer}).
		Return(&authv1.GetUserResponse{UserId: peer, Username: "bob"}, nil).Once()
	rec := f.get("tok", peer)
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"user_id":"`+peer+`","username":"bob"}`, rec.Body.String())
	_, _, errs := f.cache.Counts()
	require.Equal(t, uint64(2), errs, "failed read and failed write")
}
