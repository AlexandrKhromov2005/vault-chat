package integration

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/repository"
)

func TestChatMembers_ListedOnlyToMembersInUserOrder(t *testing.T) {
	pool := newChatPool(t)
	repo := repository.NewRoomRepository(pool)
	ctx := context.Background()
	owner, outsider := uuid.NewString(), uuid.NewString()
	room, err := repo.CreateChannel(ctx, owner, "roster", true)
	require.NoError(t, err)
	want := []string{owner}
	for range 4 {
		member := uuid.NewString()
		require.NoError(t, repo.AddChannelMember(ctx, room.ID, owner, member))
		want = append(want, member)
	}
	sort.Strings(want)
	var got []string
	after := ""
	for {
		page, err := repo.ListMembers(ctx, room.ID, want[1], after, 2)
		require.NoError(t, err)
		if len(page) == 0 {
			break
		}
		for _, member := range page {
			require.False(t, member.JoinedAt.IsZero())
			got = append(got, member.UserID)
			after = member.UserID
		}
	}
	require.Equal(t, want, got)
	for _, id := range []string{room.ID, uuid.NewString()} {
		_, err = repo.ListMembers(ctx, id, outsider, "", 10)
		require.ErrorIs(t, err, domain.ErrNotFound)
	}
	page, err := repo.ListMembers(ctx, room.ID, owner, want[len(want)-1], 10)
	require.NoError(t, err)
	require.Empty(t, page)
	direct, err := repo.GetOrCreateDirect(ctx, owner, outsider)
	require.NoError(t, err)
	page, err = repo.ListMembers(ctx, direct.ID, outsider, "", 10)
	require.NoError(t, err)
	require.Len(t, page, 2)
}

func TestChatMembers_OwnerRemovesAndAccessEndsImmediately(t *testing.T) {
	pool := newChatPool(t)
	repo := repository.NewRoomRepository(pool)
	ctx := context.Background()
	owner, member, other, outsider := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	room, err := repo.CreateChannel(ctx, owner, "team", false)
	require.NoError(t, err)
	require.NoError(t, repo.AddChannelMember(ctx, room.ID, owner, member))
	require.NoError(t, repo.AddChannelMember(ctx, room.ID, owner, other))
	_, err = repo.SendMessage(ctx, room.ID, member, uuid.NewString(), "before removal")
	require.NoError(t, err)
	require.ErrorIs(t, repo.RemoveChannelMember(ctx, room.ID, member, other), domain.ErrForbidden)
	require.ErrorIs(t, repo.RemoveChannelMember(ctx, room.ID, outsider, member), domain.ErrNotFound)
	require.ErrorIs(t, repo.RemoveChannelMember(ctx, room.ID, owner, owner), domain.ErrOwnerMembership)
	require.NoError(t, repo.RemoveChannelMember(ctx, room.ID, owner, member))
	require.NoError(t, repo.RemoveChannelMember(ctx, room.ID, owner, member))
	require.NoError(t, repo.RemoveChannelMember(ctx, room.ID, owner, outsider))
	_, err = repo.GetForMember(ctx, room.ID, member)
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = repo.ListMessages(ctx, room.ID, member, 0, 10)
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = repo.SendMessage(ctx, room.ID, member, uuid.NewString(), "after removal")
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = repo.ListMembers(ctx, room.ID, member, "", 10)
	require.ErrorIs(t, err, domain.ErrNotFound)
	rooms, err := repo.ListForMember(ctx, member, domain.RoomPosition{}, 10)
	require.NoError(t, err)
	require.Empty(t, rooms)
	members, err := repo.ListMembers(ctx, room.ID, owner, "", 10)
	require.NoError(t, err)
	require.Len(t, members, 2)
	direct, err := repo.GetOrCreateDirect(ctx, owner, member)
	require.NoError(t, err)
	require.ErrorIs(t, repo.RemoveChannelMember(ctx, direct.ID, owner, member), domain.ErrForbidden)
	_, err = repo.GetForMember(ctx, direct.ID, member)
	require.NoError(t, err)
}

func TestChatMembers_LeaveChannelButNotDialogOrOwnership(t *testing.T) {
	pool := newChatPool(t)
	repo := repository.NewRoomRepository(pool)
	ctx := context.Background()
	owner, member, outsider := uuid.NewString(), uuid.NewString(), uuid.NewString()
	room, err := repo.CreateChannel(ctx, owner, "leaving", true)
	require.NoError(t, err)
	require.NoError(t, repo.AddChannelMember(ctx, room.ID, owner, member))
	require.ErrorIs(t, repo.LeaveRoom(ctx, room.ID, owner), domain.ErrOwnerMembership)
	require.ErrorIs(t, repo.LeaveRoom(ctx, room.ID, outsider), domain.ErrNotFound)
	require.ErrorIs(t, repo.LeaveRoom(ctx, uuid.NewString(), member), domain.ErrNotFound)
	require.NoError(t, repo.LeaveRoom(ctx, room.ID, member))
	require.ErrorIs(t, repo.LeaveRoom(ctx, room.ID, member), domain.ErrNotFound)
	_, err = repo.ListMessages(ctx, room.ID, member, 0, 10)
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = repo.GetForMember(ctx, room.ID, owner)
	require.NoError(t, err)
	// A former member can be invited again.
	require.NoError(t, repo.AddChannelMember(ctx, room.ID, owner, member))
	_, err = repo.ListMessages(ctx, room.ID, member, 0, 10)
	require.NoError(t, err)
	direct, err := repo.GetOrCreateDirect(ctx, owner, member)
	require.NoError(t, err)
	require.ErrorIs(t, repo.LeaveRoom(ctx, direct.ID, member), domain.ErrForbidden)
	members, err := repo.ListMembers(ctx, direct.ID, owner, "", 10)
	require.NoError(t, err)
	require.Len(t, members, 2)
}

// behindRoomLock runs op while another transaction holds the room lock and has
// already changed membership, and commits that change once op waits for the lock.
func behindRoomLock(t *testing.T, pool *pgxpool.Pool, roomID, change string, args []any, op func() error) error {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	var blocker int32
	require.NoError(t, tx.QueryRow(ctx, `SELECT pg_backend_pid() FROM rooms WHERE id=$1::uuid FOR UPDATE`, roomID).Scan(&blocker))
	_, err = tx.Exec(ctx, change, args...)
	require.NoError(t, err)
	result := make(chan error, 1)
	go func() { result <- op() }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting bool
		require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
 WHERE wait_event_type='Lock' AND $1::int = ANY(pg_blocking_pids(pid)))`, blocker).Scan(&waiting))
		if waiting {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("operation finished without waiting for the room lock: %v", err)
		default:
		}
		require.True(t, time.Now().Before(deadline), "operation never waited for the room lock")
		time.Sleep(10 * time.Millisecond)
	}
	require.NoError(t, tx.Commit(ctx))
	select {
	case err := <-result:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("operation did not finish after the room lock was released")
		return nil
	}
}

func TestChatMembers_WaitersSeeMembershipCommittedBeforeTheLock(t *testing.T) {
	pool := newChatPool(t)
	repo := repository.NewRoomRepository(pool)
	ctx := context.Background()
	owner, member, invited := uuid.NewString(), uuid.NewString(), uuid.NewString()
	room, err := repo.CreateChannel(ctx, owner, "racing", false)
	require.NoError(t, err)
	remove := `DELETE FROM room_members WHERE room_id=$1::uuid AND user_id=$2::uuid`
	add := `INSERT INTO room_members (room_id, user_id) VALUES ($1::uuid, $2::uuid)`
	countSent := func(sender string) int {
		var count int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM messages
 WHERE room_id=$1::uuid AND sender_id=$2::uuid`, room.ID, sender).Scan(&count))
		return count
	}

	t.Run("send after removal", func(t *testing.T) {
		require.NoError(t, repo.AddChannelMember(ctx, room.ID, owner, member))
		err := behindRoomLock(t, pool, room.ID, remove, []any{room.ID, member}, func() error {
			_, err := repo.SendMessage(ctx, room.ID, member, uuid.NewString(), "too late")
			return err
		})
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.Zero(t, countSent(member))
	})
	t.Run("leave after removal", func(t *testing.T) {
		require.NoError(t, repo.AddChannelMember(ctx, room.ID, owner, member))
		err := behindRoomLock(t, pool, room.ID, remove, []any{room.ID, member}, func() error {
			return repo.LeaveRoom(ctx, room.ID, member)
		})
		require.ErrorIs(t, err, domain.ErrNotFound)
	})
	t.Run("send after enrollment", func(t *testing.T) {
		err := behindRoomLock(t, pool, room.ID, add, []any{room.ID, invited}, func() error {
			_, err := repo.SendMessage(ctx, room.ID, invited, uuid.NewString(), "just in time")
			return err
		})
		require.NoError(t, err)
		require.Equal(t, 1, countSent(invited))
	})
}
