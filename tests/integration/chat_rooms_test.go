package integration

import (
	"context"
	"os"
	"strings"
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

func TestChatDirect_ConcurrentCreation(t *testing.T) {
	pool := newChatPool(t)
	repo := repository.NewRoomRepository(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, second := uuid.NewString(), uuid.NewString()
	type result struct {
		room *domain.Room
		err  error
	}
	results := make(chan result, 20)
	start := make(chan struct{})
	for i := 0; i < cap(results); i++ {
		go func(reverse bool) {
			<-start
			a, b := first, second
			if reverse {
				a, b = strings.ReplaceAll(second, "-", ""), strings.ToUpper(first)
			}
			room, err := repo.GetOrCreateDirect(ctx, a, b)
			results <- result{room, err}
		}(i%2 == 0)
	}
	close(start)
	var id string
	for i := 0; i < cap(results); i++ {
		got := <-results
		require.NoError(t, got.err)
		if id == "" {
			id = got.room.ID
		}
		require.Equal(t, id, got.room.ID)
	}
	var rooms, members int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM rooms").Scan(&rooms))
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM room_members").Scan(&members))
	require.Equal(t, 1, rooms)
	require.Equal(t, 2, members)
}

func TestChatChannel_FailedEnrollmentRollsBackRoom(t *testing.T) {
	pool := newChatPool(t)
	ctx := context.Background()
	// Inject a database failure during the second write of channel creation.
	_, err := pool.Exec(ctx, `CREATE FUNCTION reject_chat_member() RETURNS trigger LANGUAGE plpgsql AS
 $$ BEGIN RAISE EXCEPTION 'injected membership failure'; END; $$;
 CREATE TRIGGER reject_chat_member BEFORE INSERT ON room_members FOR EACH ROW EXECUTE FUNCTION reject_chat_member();`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(), "DROP TRIGGER reject_chat_member ON room_members; DROP FUNCTION reject_chat_member()")
		require.NoError(t, err)
	})
	_, err = repository.NewRoomRepository(pool).CreateChannel(ctx, uuid.NewString(), "general", true)
	require.Error(t, err)
	var count int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM rooms").Scan(&count))
	require.Zero(t, count)
}

func TestChatMigrations_IdempotentAndReversible(t *testing.T) {
	pool := newChatPool(t)
	ctx := context.Background()
	require.NoError(t, migration.Apply(ctx, pool, migrations.ChatFS, "chat"))
	down, err := migrations.ChatFS.ReadFile("chat/0001_create_rooms.down.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(down))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "DELETE FROM schema_migrations WHERE version='0001_create_rooms.up.sql'")
	require.NoError(t, err)
	require.NoError(t, migration.Apply(ctx, pool, migrations.ChatFS, "chat"))
	room, err := repository.NewRoomRepository(pool).CreateChannel(ctx, uuid.NewString(), "recreated", false)
	require.NoError(t, err)
	require.NotEmpty(t, room.ID)
}
