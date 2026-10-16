package handler_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler/mocks"
)

func TestRoomsREST_ListsSummariesWithParticipants(t *testing.T) {
	client := mocks.NewMockChatClient(t)
	h := handler.NewChatHandler(client, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	cursor := "1792054800123456_" + uuid.NewString()
	last := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	direct := &chatv1.Room{Id: "direct", Kind: chatv1.RoomKind_ROOM_KIND_DIRECT, Private: true, ParticipantIds: []string{"actor", "peer"}}
	channel := &chatv1.Room{Id: "channel", Kind: chatv1.RoomKind_ROOM_KIND_CHANNEL, Name: "general", OwnerId: "peer"}
	client.EXPECT().ListRooms(mock.Anything, &chatv1.ListRoomsRequest{Cursor: cursor, Limit: 2}).Return(&chatv1.ListRoomsResponse{
		Rooms:      []*chatv1.RoomSummary{{Room: direct, LastMessageAt: timestamppb.New(last)}, {Room: channel}},
		NextCursor: "next",
	}, nil).Once()
	req := chatRequest(http.MethodGet, "")
	req.URL.RawQuery = "cursor=" + cursor + "&limit=2"
	rec := httptest.NewRecorder()
	h.Rooms(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var page struct {
		Rooms      []map[string]any `json:"rooms"`
		NextCursor string           `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.Equal(t, "next", page.NextCursor)
	require.Len(t, page.Rooms, 2)
	require.Equal(t, "direct", page.Rooms[0]["room_id"])
	require.Equal(t, []any{"actor", "peer"}, page.Rooms[0]["participant_ids"])
	require.Equal(t, "2026-10-15T09:00:00Z", page.Rooms[0]["last_message_at"])
	require.Equal(t, "general", page.Rooms[1]["name"])
	require.NotContains(t, page.Rooms[1], "participant_ids")
	require.NotContains(t, page.Rooms[1], "last_message_at")
	client.EXPECT().ListRooms(mock.Anything, &chatv1.ListRoomsRequest{}).Return(&chatv1.ListRoomsResponse{}, nil).Once()
	rec = httptest.NewRecorder()
	h.Rooms(rec, chatRequest(http.MethodGet, ""))
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"rooms":[],"next_cursor":""}`, rec.Body.String())
}

func TestRoomsREST_RejectsInvalidQueriesAndMapsErrors(t *testing.T) {
	client := mocks.NewMockChatClient(t)
	h := handler.NewChatHandler(client, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	rec := httptest.NewRecorder()
	h.Rooms(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	id := uuid.NewString()
	for _, query := range []string{"limit=0", "limit=101", "limit=01", "limit=1&limit=1", "cursor=5", "cursor=1_" + id + "&cursor=1_" + id, "cursor=01_" + id, "user_id=" + id, "cursor=%zz"} {
		req := chatRequest(http.MethodGet, "")
		req.URL.RawQuery = query
		rec := httptest.NewRecorder()
		h.Rooms(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, query)
		require.Contains(t, rec.Body.String(), "invalid room list query")
	}
	require.Empty(t, client.Calls)
	client.EXPECT().ListRooms(mock.Anything, mock.Anything).Return(nil, status.Error(codes.Unavailable, "private address")).Once()
	rec = httptest.NewRecorder()
	h.Rooms(rec, chatRequest(http.MethodGet, ""))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.NotContains(t, rec.Body.String(), "private")
}

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
