package handler_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler/mocks"
)

func TestRoomREST_ReportsDirectParticipants(t *testing.T) {
	client := mocks.NewMockChatClient(t)
	h := handler.NewChatHandler(client, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	room := &chatv1.Room{Id: "room", Kind: chatv1.RoomKind_ROOM_KIND_DIRECT, Private: true, ParticipantIds: []string{"actor", "peer"}}
	client.EXPECT().GetOrCreateDirect(mock.Anything, &chatv1.GetOrCreateDirectRequest{PeerUserId: "peer"}).Return(&chatv1.GetOrCreateDirectResponse{Room: room}, nil).Once()
	rec := httptest.NewRecorder()
	h.Direct(rec, chatRequest(http.MethodPost, `{"peer_user_id":"peer"}`))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"participant_ids":["actor","peer"]`)
}
