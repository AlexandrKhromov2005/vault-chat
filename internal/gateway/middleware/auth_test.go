package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware/mocks"
)

func TestAuthenticate(t *testing.T) {
	expiresAt := time.Date(2026, 10, 7, 12, 15, 0, 0, time.UTC)

	serve := func(validator middleware.TokenValidator, authorization string) (*httptest.ResponseRecorder, *middleware.Identity) {
		var identity *middleware.Identity
		h := middleware.Authenticate(validator)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if id, ok := middleware.IdentityFromContext(r.Context()); ok {
				identity = &id
			}
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec, identity
	}

	t.Run("valid token", func(t *testing.T) {
		validator := mocks.NewMockTokenValidator(t)
		validator.EXPECT().
			ValidateToken(mock.Anything, &authv1.ValidateTokenRequest{Token: "tok"}).
			Return(&authv1.ValidateTokenResponse{
				Valid:     true,
				UserId:    "u-1",
				Email:     "user@example.com",
				Username:  "username1",
				ExpiresAt: timestamppb.New(expiresAt),
			}, nil)

		rec, identity := serve(validator, "Bearer tok")

		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, &middleware.Identity{
			UserID:    "u-1",
			Email:     "user@example.com",
			Username:  "username1",
			ExpiresAt: expiresAt,
			Token:     "tok",
		}, identity)
	})

	t.Run("scheme is case-insensitive", func(t *testing.T) {
		validator := mocks.NewMockTokenValidator(t)
		validator.EXPECT().
			ValidateToken(mock.Anything, &authv1.ValidateTokenRequest{Token: "tok"}).
			Return(&authv1.ValidateTokenResponse{Valid: true, UserId: "u-1"}, nil)

		rec, _ := serve(validator, "bearer tok")
		require.Equal(t, http.StatusOK, rec.Code)
	})

	for name, header := range map[string]string{
		"missing header":         "",
		"basic scheme":           "Basic dXNlcjpwYXNz",
		"empty token":            "Bearer ",
		"no space":               "Bearertok",
		"token with extra part":  "Bearer tok extra",
		"control byte in token":  "Bearer tok\x01",
		"double space before it": "Bearer  tok",
	} {
		t.Run(name+" is rejected without calling auth", func(t *testing.T) {
			rec, identity := serve(mocks.NewMockTokenValidator(t), header)

			require.Equal(t, http.StatusUnauthorized, rec.Code)
			require.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"))
			require.JSONEq(t, `{"error":{"code":"unauthenticated","message":"missing or malformed bearer token"}}`,
				rec.Body.String())
			require.Nil(t, identity)
		})
	}

	t.Run("token rejected by auth", func(t *testing.T) {
		validator := mocks.NewMockTokenValidator(t)
		validator.EXPECT().ValidateToken(mock.Anything, mock.Anything).
			Return(&authv1.ValidateTokenResponse{Valid: false}, nil)

		rec, identity := serve(validator, "Bearer expired")

		require.Equal(t, http.StatusUnauthorized, rec.Code)
		require.Equal(t, `Bearer error="invalid_token"`, rec.Header().Get("WWW-Authenticate"))
		require.JSONEq(t, `{"error":{"code":"unauthenticated","message":"invalid or expired token"}}`,
			rec.Body.String())
		require.Nil(t, identity)
	})

	t.Run("auth unavailable", func(t *testing.T) {
		validator := mocks.NewMockTokenValidator(t)
		validator.EXPECT().ValidateToken(mock.Anything, mock.Anything).
			Return(nil, status.Error(codes.Unavailable, "connection refused"))

		rec, identity := serve(validator, "Bearer tok")

		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.Nil(t, identity)
	})
}

func TestIdentityFromContext_Empty(t *testing.T) {
	_, ok := middleware.IdentityFromContext(httptest.NewRequest(http.MethodGet, "/", nil).Context())
	require.False(t, ok)
}
