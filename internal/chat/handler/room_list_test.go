package handler_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
)

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
