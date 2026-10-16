package handler_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/validator"
)

func TestListRooms_MapsSummariesForVerifiedActor(t *testing.T) {
	h, rooms, auth := newHandler(t)
	ctx, actor, peer := authorizedContext(), uuid.NewString(), uuid.NewString()
	auth.EXPECT().ValidateToken(ctx, &authv1.ValidateTokenRequest{Token: "access"}).Return(&authv1.ValidateTokenResponse{Valid: true, UserId: actor}, nil).Once()
	last := time.Date(2026, 10, 15, 9, 0, 0, 123456000, time.UTC)
	direct := &domain.RoomSummary{Room: domain.Room{ID: uuid.NewString(), Kind: domain.Direct, Private: true, ParticipantIDs: []string{actor, peer}}, LastMessageAt: &last, Activity: last}
	channel := &domain.RoomSummary{Room: domain.Room{ID: uuid.NewString(), Kind: domain.Channel, Name: "general", OwnerID: peer}, Activity: last.Add(-time.Hour)}
	rooms.EXPECT().ListRooms(ctx, actor, "cursor", 2).Return(&service.RoomPage{Rooms: []*domain.RoomSummary{direct, channel}, NextCursor: "next"}, nil).Once()
	resp, err := h.ListRooms(ctx, &chatv1.ListRoomsRequest{Cursor: "cursor", Limit: 2})
	require.NoError(t, err)
	require.Equal(t, "next", resp.GetNextCursor())
	require.Len(t, resp.GetRooms(), 2)
	require.Equal(t, direct.ID, resp.GetRooms()[0].GetRoom().GetId())
	require.Equal(t, []string{actor, peer}, resp.GetRooms()[0].GetRoom().GetParticipantIds())
	require.True(t, last.Equal(resp.GetRooms()[0].GetLastMessageAt().AsTime()))
	require.Equal(t, "general", resp.GetRooms()[1].GetRoom().GetName())
	require.Empty(t, resp.GetRooms()[1].GetRoom().GetParticipantIds())
	require.Nil(t, resp.GetRooms()[1].GetLastMessageAt())
	_, err = h.ListRooms(context.Background(), &chatv1.ListRoomsRequest{})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestGetRoom_ReportsDirectParticipants(t *testing.T) {
	h, rooms, auth := newHandler(t)
	ctx, actor, peer := authorizedContext(), uuid.NewString(), uuid.NewString()
	auth.EXPECT().ValidateToken(ctx, mock.Anything).Return(&authv1.ValidateTokenResponse{Valid: true, UserId: actor}, nil).Once()
	room := &domain.Room{ID: uuid.NewString(), Kind: domain.Direct, Private: true, ParticipantIDs: []string{peer, actor}}
	rooms.EXPECT().GetRoom(ctx, actor, room.ID).Return(room, nil).Once()
	resp, err := h.GetRoom(ctx, &chatv1.GetRoomRequest{RoomId: room.ID})
	require.NoError(t, err)
	require.Equal(t, []string{peer, actor}, resp.GetRoom().GetParticipantIds())
}

func TestListRooms_ClassifiesInvalidPages(t *testing.T) {
	for _, tc := range []struct {
		err     error
		message string
	}{
		{validator.ErrInvalidRoomCursor, "invalid room list cursor"},
		{service.ErrInvalidRoomLimit, "room list limit must be 1–100"},
		{validator.ErrInvalidID, "invalid identifier"},
	} {
		t.Run(tc.message, func(t *testing.T) {
			h, rooms, auth := newHandler(t)
			ctx, actor := authorizedContext(), uuid.NewString()
			auth.EXPECT().ValidateToken(ctx, mock.Anything).Return(&authv1.ValidateTokenResponse{Valid: true, UserId: actor}, nil).Once()
			rooms.EXPECT().ListRooms(ctx, actor, "", 0).Return(nil, tc.err).Once()
			_, err := h.ListRooms(ctx, &chatv1.ListRoomsRequest{})
			require.Equal(t, codes.InvalidArgument, status.Code(err))
			require.Equal(t, tc.message, status.Convert(err).Message())
		})
	}
}
