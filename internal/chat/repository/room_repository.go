// Package repository persists rooms in Chat's own PostgreSQL database.
package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
)

// RoomRepository implements room storage and atomic membership authorization.
type RoomRepository struct{ pool *pgxpool.Pool }

// NewRoomRepository binds the repository to Chat's database pool.
func NewRoomRepository(pool *pgxpool.Pool) *RoomRepository { return &RoomRepository{pool: pool} }

const roomColumns = `id::text, kind, COALESCE(name, ''), COALESCE(owner_id::text, ''), private, created_at`

// GetOrCreateDirect creates both memberships atomically. PostgreSQL compares the
// UUIDs, so input ordering and alternate textual encodings cannot create duplicates.
func (r *RoomRepository) GetOrCreateDirect(ctx context.Context, first, second string) (*domain.Room, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin direct room: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	room, err := scanRoom(tx.QueryRow(ctx, `INSERT INTO rooms
  (id, kind, private, direct_user_low, direct_user_high)
  VALUES ($1::uuid, 'direct', true, LEAST($2::uuid, $3::uuid), GREATEST($2::uuid, $3::uuid))
  ON CONFLICT ON CONSTRAINT rooms_direct_pair DO UPDATE SET id = rooms.id
  RETURNING `+roomColumns, uuid.NewString(), first, second))
	if err != nil {
		return nil, fmt.Errorf("store direct room: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO room_members (room_id, user_id)
  VALUES ($1::uuid, $2::uuid), ($1::uuid, $3::uuid) ON CONFLICT DO NOTHING`, room.ID, first, second); err != nil {
		return nil, fmt.Errorf("enroll direct participants: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit direct room: %w", err)
	}
	return room, nil
}

// GetForMember performs membership filtering in the same query as the read.
// A nonmember cannot distinguish an existing room from an absent one.
func (r *RoomRepository) GetForMember(ctx context.Context, roomID, userID string) (*domain.Room, error) {
	room, err := scanRoom(r.pool.QueryRow(ctx, `SELECT `+roomColumns+` FROM rooms
  WHERE id=$1::uuid AND EXISTS (SELECT 1 FROM room_members WHERE room_id=rooms.id AND user_id=$2::uuid)`, roomID, userID))
	if err != nil {
		return nil, fmt.Errorf("get member room: %w", err)
	}
	return room, nil
}

// AddChannelMember locks the room while checking membership and ownership and
// writing the new membership. There is no unchecked enrollment method.
func (r *RoomRepository) AddChannelMember(ctx context.Context, roomID, actorID, userID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin channel enrollment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	room, err := scanRoom(tx.QueryRow(ctx, `SELECT `+roomColumns+` FROM rooms
  WHERE id=$1::uuid AND EXISTS (SELECT 1 FROM room_members WHERE room_id=rooms.id AND user_id=$2::uuid)
  FOR UPDATE`, roomID, actorID))
	if err != nil {
		return fmt.Errorf("load managed room: %w", err)
	}
	// Compare UUID values rather than the caller's textual encoding.
	owner, err := uuid.Parse(room.OwnerID)
	actor, actorErr := uuid.Parse(actorID)
	if room.Kind != domain.Channel || err != nil || actorErr != nil || owner != actor {
		return domain.ErrForbidden
	}
	if _, err := tx.Exec(ctx, `INSERT INTO room_members (room_id, user_id)
  VALUES ($1::uuid, $2::uuid) ON CONFLICT DO NOTHING`, roomID, userID); err != nil {
		return fmt.Errorf("add channel member: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit channel enrollment: %w", err)
	}
	return nil
}

func scanRoom(row pgx.Row) (*domain.Room, error) {
	var room domain.Room
	if err := row.Scan(&room.ID, &room.Kind, &room.Name, &room.OwnerID, &room.Private, &room.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scan room: %w", err)
	}
	return &room, nil
}
