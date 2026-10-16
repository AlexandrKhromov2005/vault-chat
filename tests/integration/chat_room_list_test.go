package integration

import (
	"context"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
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
	rooms, err := repo.ListForMember(ctx, a, domain.RoomPosition{}, 10)
	require.NoError(t, err)
	require.Len(t, rooms, 2)
	require.Equal(t, channel.ID, rooms[0].ID)
	require.Empty(t, rooms[0].ParticipantIDs)
	require.Equal(t, want, rooms[1].ParticipantIDs)
}

func TestChatRooms_ListByActivityWithKeysetPages(t *testing.T) {
	pool := newChatPool(t)
	repo := repository.NewRoomRepository(pool)
	ctx := context.Background()
	a, b, c, outsider := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	direct, err := repo.GetOrCreateDirect(ctx, a, b)
	require.NoError(t, err)
	own, err := repo.CreateChannel(ctx, a, "own", true)
	require.NoError(t, err)
	foreign, err := repo.CreateChannel(ctx, c, "foreign", true)
	require.NoError(t, err)
	_, err = repo.CreateChannel(ctx, c, "hidden", true)
	require.NoError(t, err)
	// A later invitation surfaces an old quiet channel above earlier rooms.
	require.NoError(t, repo.AddChannelMember(ctx, foreign.ID, c, a))
	message, err := repo.SendMessage(ctx, direct.ID, b, uuid.NewString(), "hello")
	require.NoError(t, err)
	rooms, err := repo.ListForMember(ctx, a, domain.RoomPosition{}, 10)
	require.NoError(t, err)
	require.Equal(t, []string{direct.ID, foreign.ID, own.ID}, summaryIDs(rooms))
	require.NotNil(t, rooms[0].LastMessageAt)
	require.True(t, message.CreatedAt.Equal(*rooms[0].LastMessageAt))
	require.True(t, message.CreatedAt.Equal(rooms[0].Activity))
	require.Nil(t, rooms[1].LastMessageAt)
	require.Nil(t, rooms[2].LastMessageAt)
	require.True(t, rooms[2].Activity.Equal(own.CreatedAt))
	require.Equal(t, own, &rooms[2].Room)
	var paged []string
	position := domain.RoomPosition{}
	for {
		page, err := repo.ListForMember(ctx, a, position, 1)
		require.NoError(t, err)
		if len(page) == 0 {
			break
		}
		require.Len(t, page, 1)
		paged = append(paged, page[0].ID)
		position = domain.RoomPosition{Activity: page[0].Activity, ID: page[0].ID}
	}
	require.Equal(t, summaryIDs(rooms), paged)
	rooms, err = repo.ListForMember(ctx, b, domain.RoomPosition{}, 10)
	require.NoError(t, err)
	require.Equal(t, []string{direct.ID}, summaryIDs(rooms))
	rooms, err = repo.ListForMember(ctx, outsider, domain.RoomPosition{}, 10)
	require.NoError(t, err)
	require.Empty(t, rooms)
}

func TestChatRooms_EqualActivityPagesByRoomID(t *testing.T) {
	pool := newChatPool(t)
	repo := repository.NewRoomRepository(pool)
	ctx := context.Background()
	owner := uuid.NewString()
	var ids []string
	for range 5 {
		room, err := repo.CreateChannel(ctx, owner, "tie", false)
		require.NoError(t, err)
		ids = append(ids, room.ID)
	}
	_, err := pool.Exec(ctx, "UPDATE room_members SET joined_at='2026-01-02T03:04:05.123456Z'")
	require.NoError(t, err)
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	var paged []string
	position := domain.RoomPosition{}
	for len(paged) <= len(ids) {
		page, err := repo.ListForMember(ctx, owner, position, 2)
		require.NoError(t, err)
		if len(page) == 0 {
			break
		}
		for _, room := range page {
			paged = append(paged, room.ID)
			position = domain.RoomPosition{Activity: room.Activity, ID: room.ID}
		}
	}
	require.Equal(t, ids, paged)
}

func summaryIDs(rooms []*domain.RoomSummary) []string {
	ids := make([]string, 0, len(rooms))
	for _, room := range rooms {
		ids = append(ids, room.ID)
	}
	return ids
}
