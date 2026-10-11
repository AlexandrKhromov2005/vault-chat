package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/repository"
)

func TestChatMessages_MembershipRetriesAndPagination(t *testing.T) {
	pool := newChatPool(t)
	repo := repository.NewRoomRepository(pool)
	ctx := context.Background()
	a, b, outsider := uuid.NewString(), uuid.NewString(), uuid.NewString()
	room, err := repo.GetOrCreateDirect(ctx, a, b)
	require.NoError(t, err)
	key := uuid.NewString()
	first, err := repo.SendMessage(ctx, room.ID, a, key, "hello")
	require.NoError(t, err)
	retry, err := repo.SendMessage(ctx, room.ID, a, key, "hello")
	require.NoError(t, err)
	require.Equal(t, first, retry)
	_, err = repo.SendMessage(ctx, room.ID, a, key, "changed")
	require.ErrorIs(t, err, domain.ErrMessageConflict)
	_, err = repo.SendMessage(ctx, room.ID, outsider, key, "hello")
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = repo.ListMessages(ctx, room.ID, outsider, 0, 10)
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = repo.ListMessages(ctx, uuid.NewString(), outsider, 0, 10)
	require.ErrorIs(t, err, domain.ErrNotFound)
	second, err := repo.SendMessage(ctx, room.ID, b, key, "reply")
	require.NoError(t, err)
	require.Greater(t, second.Sequence, first.Sequence)
	page, err := repo.ListMessages(ctx, room.ID, a, 0, 1)
	require.NoError(t, err)
	require.Equal(t, second, page[0])
	page, err = repo.ListMessages(ctx, room.ID, a, second.Sequence, 10)
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Equal(t, first, page[0])
}

func TestChatMessages_ConcurrentRetries(t *testing.T) {
	pool := newChatPool(t)
	repo := repository.NewRoomRepository(pool)
	ctx := context.Background()
	a, b := uuid.NewString(), uuid.NewString()
	room, err := repo.GetOrCreateDirect(ctx, a, b)
	require.NoError(t, err)
	key := uuid.NewString()
	type result struct {
		message *domain.Message
		err     error
	}
	results := make(chan result, 20)
	for range cap(results) {
		go func() { m, err := repo.SendMessage(ctx, room.ID, a, key, "once"); results <- result{m, err} }()
	}
	var first *domain.Message
	for range cap(results) {
		got := <-results
		require.NoError(t, got.err)
		if first == nil {
			first = got.message
		}
		require.Equal(t, first, got.message)
	}
	page, err := repo.ListMessages(ctx, room.ID, b, 0, 100)
	require.NoError(t, err)
	require.Len(t, page, 1)
}
