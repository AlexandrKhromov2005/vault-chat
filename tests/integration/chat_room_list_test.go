package integration

import (
	"context"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/repository"
)

func TestChatRooms_DirectParticipantsOnEveryRead(t *testing.T) {
	pool := newChatPool(t)
	repo := repository.NewRoomRepository(pool)
	ctx := context.Background()
	a, b := uuid.NewString(), uuid.NewString()
	want := []string{a, b}
	sort.Strings(want)
	room, err := repo.GetOrCreateDirect(ctx, a, b)
	require.NoError(t, err)
	require.Equal(t, want, room.ParticipantIDs)
	got, err := repo.GetForMember(ctx, room.ID, b)
	require.NoError(t, err)
	require.Equal(t, want, got.ParticipantIDs)
	channel, err := repo.CreateChannel(ctx, a, "general", false)
	require.NoError(t, err)
	require.Empty(t, channel.ParticipantIDs)
}
