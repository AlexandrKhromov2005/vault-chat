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

func TestChatMessages_FailedWriteRollsBackSequence(t *testing.T) {
	pool := newChatPool(t)
	repo := repository.NewRoomRepository(pool)
	ctx := context.Background()
	a := uuid.NewString()
	room, err := repo.CreateChannel(ctx, a, "test", true)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `CREATE FUNCTION reject_message() RETURNS trigger LANGUAGE plpgsql AS
 $$ BEGIN RAISE EXCEPTION 'injected write failure'; END; $$;
 CREATE TRIGGER reject_message BEFORE INSERT ON messages FOR EACH ROW EXECUTE FUNCTION reject_message();`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(), "DROP FUNCTION IF EXISTS reject_message() CASCADE")
		require.NoError(t, err)
	})
	_, err = repo.SendMessage(ctx, room.ID, a, uuid.NewString(), "hello")
	require.Error(t, err)
	var sequence int64
	require.NoError(t, pool.QueryRow(ctx, "SELECT message_sequence FROM rooms WHERE id=$1", room.ID).Scan(&sequence))
	require.Zero(t, sequence)
	_, err = pool.Exec(ctx, "DROP TRIGGER reject_message ON messages")
	require.NoError(t, err)
	message, err := repo.SendMessage(ctx, room.ID, a, uuid.NewString(), "hello")
	require.NoError(t, err)
	require.Equal(t, int64(1), message.Sequence)
}

func TestChatMessages_ConcurrentDistinctSends(t *testing.T) {
	pool := newChatPool(t)
	repo := repository.NewRoomRepository(pool)
	ctx := context.Background()
	a := uuid.NewString()
	room, err := repo.CreateChannel(ctx, a, "concurrent", true)
	require.NoError(t, err)
	type result struct {
		message *domain.Message
		err     error
	}
	results := make(chan result, 20)
	for range cap(results) {
		go func() {
			message, err := repo.SendMessage(ctx, room.ID, a, uuid.NewString(), "new")
			results <- result{message, err}
		}()
	}
	seen := make(map[string]bool)
	for range cap(results) {
		got := <-results
		require.NoError(t, got.err)
		seen[got.message.ID] = true
	}
	require.Len(t, seen, cap(results))
	var before int64
	remaining := int64(cap(results))
	for remaining > 0 {
		page, err := repo.ListMessages(ctx, room.ID, a, before, 3)
		require.NoError(t, err)
		require.NotEmpty(t, page)
		for _, message := range page {
			require.Equal(t, remaining, message.Sequence)
			require.True(t, seen[message.ID])
			delete(seen, message.ID)
			remaining--
			before = message.Sequence
		}
	}
	require.Empty(t, seen)
}
