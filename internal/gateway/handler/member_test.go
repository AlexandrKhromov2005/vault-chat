package handler_test

import (
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

func TestMembersREST_ListRemoveAndLeave(t *testing.T) {
	client := mocks.NewMockChatClient(t)
	h := handler.NewChatHandler(client, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	cursor := uuid.NewString()
	joined := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	client.EXPECT().ListMembers(mock.Anything, &chatv1.ListMembersRequest{RoomId: "room", Cursor: cursor, Limit: 3}).Return(&chatv1.ListMembersResponse{
		Members: []*chatv1.Member{{UserId: "member", JoinedAt: timestamppb.New(joined)}}, NextCursor: "member",
	}, nil).Once()
	req := chatRequest(http.MethodGet, "")
	req.SetPathValue("room_id", "room")
	req.URL.RawQuery = "cursor=" + cursor + "&limit=3"
	rec := httptest.NewRecorder()
	h.Members(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"members":[{"user_id":"member","joined_at":"2026-10-15T09:00:00Z"}],"next_cursor":"member"}`, rec.Body.String())
	client.EXPECT().ListMembers(mock.Anything, &chatv1.ListMembersRequest{RoomId: "room"}).Return(&chatv1.ListMembersResponse{}, nil).Once()
	req.URL.RawQuery = ""
	rec = httptest.NewRecorder()
	h.Members(rec, req)
	require.JSONEq(t, `{"members":[],"next_cursor":""}`, rec.Body.String())
	client.EXPECT().RemoveMember(mock.Anything, &chatv1.RemoveMemberRequest{RoomId: "room", UserId: "member"}).Return(&chatv1.RemoveMemberResponse{}, nil).Once()
	req = chatRequest(http.MethodDelete, "")
	req.SetPathValue("room_id", "room")
	req.SetPathValue("user_id", "member")
	rec = httptest.NewRecorder()
	h.RemoveMember(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
	client.EXPECT().LeaveRoom(mock.Anything, &chatv1.LeaveRoomRequest{RoomId: "room"}).Return(nil, status.Error(codes.FailedPrecondition, "channel owner cannot leave or be removed")).Once()
	req = chatRequest(http.MethodDelete, "")
	req.SetPathValue("room_id", "room")
	rec = httptest.NewRecorder()
	h.LeaveRoom(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), `"failed_precondition"`)
}

func TestMembersREST_RejectsMissingIdentityAndInvalidQueries(t *testing.T) {
	client := mocks.NewMockChatClient(t)
	h := handler.NewChatHandler(client, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	for _, method := range []http.HandlerFunc{h.Members, h.RemoveMember, h.LeaveRoom} {
		rec := httptest.NewRecorder()
		method(rec, httptest.NewRequest(http.MethodDelete, "/", nil))
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	}
	id := uuid.NewString()
	for _, query := range []string{"limit=0", "limit=101", "cursor=1", "cursor=" + id + "&cursor=" + id, "cursor=urn:uuid:" + id, "room_id=" + id} {
		req := chatRequest(http.MethodGet, "")
		req.URL.RawQuery = query
		rec := httptest.NewRecorder()
		h.Members(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, query)
		require.Contains(t, rec.Body.String(), "invalid member list query")
	}
	require.Empty(t, client.Calls)
}
