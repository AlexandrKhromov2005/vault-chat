package router_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
)

func TestRouter_ChatRoutesRequireIdentity(t *testing.T) {
	for _, tc := range []struct{ method, path string }{{"POST", "/api/v1/rooms/direct"}, {"POST", "/api/v1/rooms/channels"}, {"GET", "/api/v1/rooms/id"}, {"PUT", "/api/v1/rooms/id/members/peer"}, {"GET", "/api/v1/users/peer"}} {
		f := newFixture(t)
		f.allow("ip:203.0.113.7")
		rec := f.serve(httptest.NewRequest(tc.method, tc.path, nil))
		require.Equal(t, http.StatusUnauthorized, rec.Code, tc.path)
	}
}

func TestRouter_RoomPathAndPerUserRateLimit(t *testing.T) {
	f := newFixture(t)
	f.allow("ip:203.0.113.7")
	f.allow("user:u-1")
	f.validator.EXPECT().ValidateToken(mock.Anything, &authv1.ValidateTokenRequest{Token: "tok"}).Return(&authv1.ValidateTokenResponse{Valid: true, UserId: "u-1"}, nil).Once()
	f.chat.EXPECT().GetRoom(mock.Anything, &chatv1.GetRoomRequest{RoomId: "room"}).Return(&chatv1.GetRoomResponse{Room: &chatv1.Room{Id: "room"}}, nil).Once()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/rooms/room", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := f.serve(req)
	require.Equal(t, http.StatusOK, rec.Code)
}
