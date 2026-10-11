package handler_test

import (
	"context"
	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler/mocks"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
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
