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
