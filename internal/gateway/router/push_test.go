package router_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
)

func TestRouter_PushTokenRoutesAreProtected(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		f := newFixture(t)
		f.allow("ip:203.0.113.7")
		r := httptest.NewRequest(method, "/api/v1/push/token", strings.NewReader(`{"provider":"fcm","token":"t"}`))
		r.Header.Set("Content-Type", "application/json")
		require.Equal(t, http.StatusUnauthorized, f.serve(r).Code, method)
	}

	f := newFixture(t)
	for range 2 {
		f.allow("ip:203.0.113.7")
		f.allow("user:u-1")
	}
	f.validator.EXPECT().ValidateToken(mock.Anything, &authv1.ValidateTokenRequest{Token: "tok"}).
		Return(&authv1.ValidateTokenResponse{Valid: true, UserId: "u-1"}, nil).Twice()
	f.auth.EXPECT().RegisterPushToken(mock.Anything, &authv1.RegisterPushTokenRequest{AccessToken: "tok", Provider: "fcm", Token: "t"}).
		Return(&authv1.RegisterPushTokenResponse{}, nil)
	f.auth.EXPECT().UnregisterPushToken(mock.Anything, &authv1.UnregisterPushTokenRequest{AccessToken: "tok"}).
		Return(&authv1.UnregisterPushTokenResponse{}, nil)
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		r := httptest.NewRequest(method, "/api/v1/push/token", strings.NewReader(`{"provider":"fcm","token":"t"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer tok")
		require.Equal(t, http.StatusNoContent, f.serve(r).Code, method)
	}

	f = newFixture(t)
	f.allow("ip:203.0.113.7")
	require.Equal(t, http.StatusNotFound, f.serve(httptest.NewRequest(http.MethodGet, "/api/v1/push/token", nil)).Code,
		"tokens are never listed")
}
