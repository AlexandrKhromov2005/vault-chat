package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
)

func pushRequest(method, body string, authenticated bool) *http.Request {
	r := httptest.NewRequest(method, "/api/v1/push/token", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if authenticated {
		r = r.WithContext(middleware.WithIdentity(context.Background(), middleware.Identity{UserID: "u-1", Token: "access-1"}))
	}
	return r
}

func TestAuthHandler_RegisterPushToken(t *testing.T) {
	t.Run("forwards the token with the caller's access token", func(t *testing.T) {
		h, client, _ := newAuthHandler(t)
		client.EXPECT().RegisterPushToken(mock.Anything, &authv1.RegisterPushTokenRequest{
			AccessToken: "access-1", Provider: "fcm", Token: "device-token",
		}).Return(&authv1.RegisterPushTokenResponse{}, nil)

		rec := httptest.NewRecorder()
		h.RegisterPushToken(rec, pushRequest(http.MethodPut, `{"provider":"fcm","token":"device-token"}`, true))
		require.Equal(t, http.StatusNoContent, rec.Code)
		require.Empty(t, rec.Body.String())
	})

	t.Run("malformed bodies never reach auth", func(t *testing.T) {
		for _, body := range []string{``, `!`, `{"provider":"fcm","token":"t","user_id":"u-2"}`, `{"token":1}`, `{} {}`} {
			h, _, _ := newAuthHandler(t)
			rec := httptest.NewRecorder()
			h.RegisterPushToken(rec, pushRequest(http.MethodPut, body, true))
			require.Equal(t, http.StatusBadRequest, rec.Code, body)
		}
	})

	t.Run("unauthenticated request is rejected without calling auth", func(t *testing.T) {
		h, _, _ := newAuthHandler(t)
		rec := httptest.NewRecorder()
		h.RegisterPushToken(rec, pushRequest(http.MethodPut, `{"provider":"fcm","token":"t"}`, false))
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("auth errors keep their meaning", func(t *testing.T) {
		for code, want := range map[codes.Code]int{
			codes.InvalidArgument: http.StatusBadRequest,
			codes.Unauthenticated: http.StatusUnauthorized,
			codes.Unavailable:     http.StatusServiceUnavailable,
			codes.Internal:        http.StatusInternalServerError,
		} {
			h, client, logs := newAuthHandler(t)
			client.EXPECT().RegisterPushToken(mock.Anything, mock.Anything).Return(nil, status.Error(code, "synthetic"))
			rec := httptest.NewRecorder()
			h.RegisterPushToken(rec, pushRequest(http.MethodPut, `{"provider":"fcm","token":"secret-device-token"}`, true))
			require.Equal(t, want, rec.Code, code.String())
			require.NotContains(t, logs.String(), "secret-device-token", "tokens are never logged")
		}
	})
}

func TestAuthHandler_UnregisterPushToken(t *testing.T) {
	h, client, _ := newAuthHandler(t)
	client.EXPECT().UnregisterPushToken(mock.Anything, &authv1.UnregisterPushTokenRequest{AccessToken: "access-1"}).
		Return(&authv1.UnregisterPushTokenResponse{}, nil)
	rec := httptest.NewRecorder()
	h.UnregisterPushToken(rec, pushRequest(http.MethodDelete, ``, true))
	require.Equal(t, http.StatusNoContent, rec.Code)

	h, client, _ = newAuthHandler(t)
	client.EXPECT().UnregisterPushToken(mock.Anything, mock.Anything).Return(nil, status.Error(codes.Unavailable, "db down"))
	rec = httptest.NewRecorder()
	h.UnregisterPushToken(rec, pushRequest(http.MethodDelete, ``, true))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	h, _, _ = newAuthHandler(t)
	rec = httptest.NewRecorder()
	h.UnregisterPushToken(rec, pushRequest(http.MethodDelete, ``, false))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
