package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
)

// ListMembers returns members ordered by user ID after an exclusive user ID.
// Membership and the page use one snapshot, so an empty page is still authorized.
func (r *RoomRepository) ListMembers(ctx context.Context, roomID, actorID, after string, limit int) ([]*domain.Member, error) {
	var cursor any
	if after != "" {
		cursor = after
	}
	rows, err := r.pool.Query(ctx, `SELECT m.user_id::text, m.joined_at FROM rooms r
 LEFT JOIN LATERAL (SELECT user_id, joined_at FROM room_members WHERE room_id=r.id
 AND ($3::uuid IS NULL OR user_id>$3::uuid) ORDER BY user_id LIMIT $4) m ON true
 WHERE r.id=$1::uuid AND EXISTS (SELECT 1 FROM room_members WHERE room_id=r.id AND user_id=$2::uuid)
 ORDER BY m.user_id`, roomID, actorID, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("query members: %w", err)
	}
	defer rows.Close()
	members := make([]*domain.Member, 0)
	authorized := false
	for rows.Next() {
		authorized = true
		// The left join has one null row for an authorized empty page.
		var userID *string
		var joined *time.Time
		if err := rows.Scan(&userID, &joined); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		if userID != nil {
			members = append(members, &domain.Member{UserID: *userID, JoinedAt: *joined})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read members: %w", err)
	}
	if !authorized {
		return nil, domain.ErrNotFound
	}
	return members, nil
}

// RemoveChannelMember lets the owner remove another member under the room lock.
// Removing a nonmember succeeds, so a retried removal is idempotent.
func (r *RoomRepository) RemoveChannelMember(ctx context.Context, roomID, actorID, userID string) error {
	return r.changeMembership(ctx, roomID, actorID, userID, domain.MemberRemoved, func(room *domain.Room) error {
		if err := requireOwner(room, actorID); err != nil {
			return err
		}
		if sameID(room.OwnerID, userID) {
			return domain.ErrOwnerMembership
		}
		return nil
	})
}

// LeaveRoom removes the actor from a channel under the room lock. Dialogs cannot
// be left and the owner stays until ownership can be transferred.
func (r *RoomRepository) LeaveRoom(ctx context.Context, roomID, actorID string) error {
	return r.changeMembership(ctx, roomID, actorID, actorID, domain.MemberLeft, func(room *domain.Room) error {
		if room.Kind != domain.Channel {
			return domain.ErrForbidden
		}
		if sameID(room.OwnerID, actorID) {
			return domain.ErrOwnerMembership
		}
		return nil
	})
}

// changeMembership deletes a membership after authorize accepts the locked room
// and announces an effective deletion as kind.
func (r *RoomRepository) changeMembership(ctx context.Context, roomID, actorID, userID string, kind domain.EventKind, authorize func(*domain.Room) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin membership change: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	room, err := lockMemberRoom(ctx, tx, roomID, actorID)
	if err != nil {
		return err
	}
	if err := authorize(room); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM room_members WHERE room_id=$1::uuid AND user_id=$2::uuid`, roomID, userID)
	if err != nil {
		return fmt.Errorf("delete membership: %w", err)
	}
	var events []domain.Event
	if tag.RowsAffected() > 0 {
		events, err = r.membershipEvent(ctx, tx, kind, room.ID, userID)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit membership change: %w", err)
	}
	r.notify(ctx, events...)
	return nil
}
