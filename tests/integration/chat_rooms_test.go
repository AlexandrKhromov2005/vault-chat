package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/migration"
	"github.com/AlexandrKhromov2005/vault-chat/migrations"
)

func newChatPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("VAULT_CHAT_TEST_CHAT_DATABASE_URL")
	if dsn == "" {
		t.Skip("VAULT_CHAT_TEST_CHAT_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, migration.Apply(ctx, pool, migrations.ChatFS, "chat"))
	_, err = pool.Exec(ctx, "TRUNCATE rooms CASCADE")
	require.NoError(t, err)
	return pool
}

func TestChatDirect_MembershipAndIdempotency(t *testing.T) {
	pool := newChatPool(t)
	repo := repository.NewRoomRepository(pool)
	ctx := context.Background()
	first, second, outsider := uuid.NewString(), uuid.NewString(), uuid.NewString()
	room, err := repo.GetOrCreateDirect(ctx, first, second)
	require.NoError(t, err)
	require.Equal(t, domain.Direct, room.Kind)
	require.True(t, room.Private)
	require.Empty(t, room.OwnerID)
	require.False(t, room.CreatedAt.IsZero())
	reversed, err := repo.GetOrCreateDirect(ctx, second, first)
	require.NoError(t, err)
	require.Equal(t, room, reversed)
	for _, member := range []string{first, second} {
		got, err := repo.GetForMember(ctx, room.ID, member)
		require.NoError(t, err)
		require.Equal(t, room, got)
	}
	_, err = repo.GetForMember(ctx, room.ID, outsider)
	require.ErrorIs(t, err, domain.ErrNotFound)
	require.ErrorIs(t, repo.AddChannelMember(ctx, room.ID, first, outsider), domain.ErrForbidden)
	_, err = repo.GetForMember(ctx, room.ID, outsider)
	require.ErrorIs(t, err, domain.ErrNotFound)
	var count int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM room_members WHERE room_id=$1", room.ID).Scan(&count))
	require.Equal(t, 2, count)
}

func TestChatChannel_OwnerControlsMembership(t *testing.T) {
	pool := newChatPool(t)
	repo := repository.NewRoomRepository(pool)
	ctx := context.Background()
	owner, member, outsider := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, private := range []bool{false, true} {
		room, err := repo.CreateChannel(ctx, owner, "Команда", private)
		require.NoError(t, err)
		require.Equal(t, domain.Channel, room.Kind)
		require.Equal(t, owner, room.OwnerID)
		require.Equal(t, private, room.Private)
		got, err := repo.GetForMember(ctx, room.ID, owner)
		require.NoError(t, err)
		require.Equal(t, room, got)
		require.ErrorIs(t, repo.AddChannelMember(ctx, room.ID, outsider, outsider), domain.ErrNotFound)
		require.NoError(t, repo.AddChannelMember(ctx, room.ID, owner, member))
		require.NoError(t, repo.AddChannelMember(ctx, room.ID, owner, member))
		require.NoError(t, repo.AddChannelMember(ctx, room.ID, owner, owner))
		_, err = repo.GetForMember(ctx, room.ID, member)
		require.NoError(t, err)
		require.ErrorIs(t, repo.AddChannelMember(ctx, room.ID, member, outsider), domain.ErrForbidden)
		_, err = repo.GetForMember(ctx, room.ID, outsider)
		require.ErrorIs(t, err, domain.ErrNotFound)
		_, err = repo.GetForMember(ctx, uuid.NewString(), outsider)
		require.ErrorIs(t, err, domain.ErrNotFound)
		var count int
		require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM room_members WHERE room_id=$1", room.ID).Scan(&count))
		require.Equal(t, 2, count)
	}
}
