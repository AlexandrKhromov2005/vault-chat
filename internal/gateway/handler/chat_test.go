package handler_test

import (
	"context"
	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler/mocks"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func chatRequest(method, body string) *http.Request {
	req := httptest.NewRequest(method, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req.WithContext(middleware.WithIdentity(req.Context(), middleware.Identity{UserID: "actor", Token: "access"}))
}

func TestChatREST_ForwardsVerifiedCredential(t *testing.T) {
	client := mocks.NewMockChatClient(t)
	h := handler.NewChatHandler(client, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	client.EXPECT().GetOrCreateDirect(mock.MatchedBy(func(ctx context.Context) bool {
		md, _ := metadata.FromOutgoingContext(ctx)
		return len(md.Get("authorization")) == 1 && md.Get("authorization")[0] == "Bearer access"
	}), &chatv1.GetOrCreateDirectRequest{PeerUserId: "peer"}).Return(&chatv1.GetOrCreateDirectResponse{Room: &chatv1.Room{Id: "room", Kind: chatv1.RoomKind_ROOM_KIND_DIRECT, Private: true}}, nil).Once()
	rec := httptest.NewRecorder()
	h.Direct(rec, chatRequest(http.MethodPost, `{"peer_user_id":"peer"}`))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"room_id":"room"`)
	require.Contains(t, rec.Body.String(), `"kind":"direct"`)
}

func TestChatREST_RejectsMissingIdentityAndCallerFields(t *testing.T) {
	client := mocks.NewMockChatClient(t)
	h := handler.NewChatHandler(client, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	for _, method := range []http.HandlerFunc{h.Direct, h.CreateChannel, h.Room, h.AddMember} {
		rec := httptest.NewRecorder()
		method(rec, httptest.NewRequest(http.MethodPost, "/", nil))
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	}
	rec := httptest.NewRecorder()
	h.Direct(rec, chatRequest(http.MethodPost, `{"peer_user_id":"peer","actor_id":"someone-else"}`))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Empty(t, client.Calls)
}

func TestChatREST_ChannelRoomAndMembership(t *testing.T) {
	client := mocks.NewMockChatClient(t)
	h := handler.NewChatHandler(client, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	room := &chatv1.Room{Id: "room", Kind: chatv1.RoomKind_ROOM_KIND_CHANNEL, Name: "general", OwnerId: "actor"}
	client.EXPECT().CreateChannel(mock.Anything, &chatv1.CreateChannelRequest{Name: "general", Private: true}).Return(&chatv1.CreateChannelResponse{Room: room}, nil).Once()
	rec := httptest.NewRecorder()
	h.CreateChannel(rec, chatRequest(http.MethodPost, `{"name":"general","private":true}`))
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Contains(t, rec.Body.String(), `"owner_id":"actor"`)
	client.EXPECT().GetRoom(mock.Anything, &chatv1.GetRoomRequest{RoomId: "room"}).Return(&chatv1.GetRoomResponse{Room: room}, nil).Once()
	req := chatRequest(http.MethodGet, "")
	req.SetPathValue("room_id", "room")
	rec = httptest.NewRecorder()
	h.Room(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	client.EXPECT().AddMember(mock.Anything, &chatv1.AddMemberRequest{RoomId: "room", UserId: "member"}).Return(&chatv1.AddMemberResponse{}, nil).Once()
	req = chatRequest(http.MethodPut, "")
	req.SetPathValue("room_id", "room")
	req.SetPathValue("user_id", "member")
	rec = httptest.NewRecorder()
	h.AddMember(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestChatREST_UsesSafeBackendErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		code codes.Code
		http int
	}{{codes.PermissionDenied, 403}, {codes.NotFound, 404}, {codes.DeadlineExceeded, 504}, {codes.Unavailable, 503}, {codes.Internal, 500}} {
		client := mocks.NewMockChatClient(t)
		client.EXPECT().GetRoom(mock.Anything, mock.Anything).Return(nil, status.Error(tc.code, "backend failure")).Once()
		h := handler.NewChatHandler(client, slog.New(slog.NewJSONHandler(io.Discard, nil)))
		rec := httptest.NewRecorder()
		h.Room(rec, chatRequest(http.MethodGet, ""))
		require.Equal(t, tc.http, rec.Code)
		if tc.http >= 500 {
			require.NotContains(t, rec.Body.String(), "backend failure")
		}
	}
}
