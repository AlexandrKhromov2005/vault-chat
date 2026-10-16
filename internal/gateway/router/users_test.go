package router_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
)

func TestRouter_UserSearchIsProtectedAndDistinctFromProfile(t *testing.T) {
	f := newFixture(t)
	f.allow("ip:203.0.113.7")
	rec := f.serve(httptest.NewRequest(http.MethodGet, "/api/v1/users?query=bo", nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	f = newFixture(t)
	for range 2 {
		f.allow("ip:203.0.113.7")
		f.allow("user:u-1")
	}
	f.validator.EXPECT().ValidateToken(mock.Anything, &authv1.ValidateTokenRequest{Token: "tok"}).
		Return(&authv1.ValidateTokenResponse{Valid: true, UserId: "u-1"}, nil).Twice()
	f.auth.EXPECT().SearchUsers(mock.Anything, &authv1.SearchUsersRequest{AccessToken: "tok", Query: "bo"}).
		Return(&authv1.SearchUsersResponse{}, nil).Once()
	f.auth.EXPECT().GetUser(mock.Anything, &authv1.GetUserRequest{AccessToken: "tok", UserId: "peer"}).
		Return(&authv1.GetUserResponse{UserId: "peer"}, nil).Once()
	for _, path := range []string{"/api/v1/users?query=bo", "/api/v1/users/peer"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer tok")
		require.Equal(t, http.StatusOK, f.serve(req).Code, path)
	}
}
