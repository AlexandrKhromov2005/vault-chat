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

// roomColumns qualifies every column so that queries can join messages and memberships.
const roomColumns = `rooms.id::text, rooms.kind, COALESCE(rooms.name, ''), COALESCE(rooms.owner_id::text, ''),
  rooms.private, rooms.created_at, COALESCE(rooms.direct_user_low::text, ''), COALESCE(rooms.direct_user_high::text, '')`

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

// ListForMember returns the member's rooms after an exclusive position, most
// recently active first. The latest message is found through its room sequence.
func (r *RoomRepository) ListForMember(ctx context.Context, userID string, after domain.RoomPosition, limit int) ([]*domain.RoomSummary, error) {
	var activity, roomID any
	if after.ID != "" {
		activity, roomID = after.Activity, after.ID
	}
	rows, err := r.pool.Query(ctx, `SELECT `+roomColumns+`, messages.created_at, activity.at FROM room_members
  JOIN rooms ON rooms.id=room_members.room_id
  LEFT JOIN messages ON messages.room_id=rooms.id AND messages.sequence=rooms.message_sequence
  CROSS JOIN LATERAL (SELECT GREATEST(messages.created_at, room_members.joined_at) AS at) activity
  WHERE room_members.user_id=$1::uuid
  AND ($2::timestamptz IS NULL OR (activity.at, rooms.id) < ($2::timestamptz, $3::uuid))
  ORDER BY activity.at DESC, rooms.id DESC LIMIT $4`, userID, activity, roomID, limit)
	if err != nil {
		return nil, fmt.Errorf("query member rooms: %w", err)
	}
	defer rows.Close()
	rooms := make([]*domain.RoomSummary, 0)
	for rows.Next() {
		summary := &domain.RoomSummary{}
		if err := rows.Scan(roomFields(&summary.Room, &summary.LastMessageAt, &summary.Activity)...); err != nil {
			return nil, fmt.Errorf("scan member room: %w", err)
		}
		withParticipants(&summary.Room)
		rooms = append(rooms, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read member rooms: %w", err)
	}
	return rooms, nil
}

// AddChannelMember locks the room while checking membership and ownership and
// writing the new membership. There is no unchecked enrollment method.
func (r *RoomRepository) AddChannelMember(ctx context.Context, roomID, actorID, userID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin channel enrollment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := lockOwnedChannel(ctx, tx, roomID, actorID); err != nil {
		return err
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

// lockMemberRoom locks a room visible to the actor until the transaction ends,
// serializing membership changes with sends and other membership changes.
func lockMemberRoom(ctx context.Context, tx pgx.Tx, roomID, actorID string) (*domain.Room, error) {
	room, err := scanRoom(tx.QueryRow(ctx, `SELECT `+roomColumns+` FROM rooms
  WHERE id=$1::uuid AND EXISTS (SELECT 1 FROM room_members WHERE room_id=rooms.id AND user_id=$2::uuid)
  FOR UPDATE`, roomID, actorID))
	if err != nil {
		return nil, fmt.Errorf("load managed room: %w", err)
	}
	return room, nil
}

// lockOwnedChannel locks a room and requires it to be a channel owned by the actor.
func lockOwnedChannel(ctx context.Context, tx pgx.Tx, roomID, actorID string) (*domain.Room, error) {
	room, err := lockMemberRoom(ctx, tx, roomID, actorID)
	if err != nil {
		return nil, err
	}
	return room, requireOwner(room, actorID)
}

// requireOwner allows only the owner of a channel to manage its members.
func requireOwner(room *domain.Room, actorID string) error {
	if room.Kind != domain.Channel || !sameID(room.OwnerID, actorID) {
		return domain.ErrForbidden
	}
	return nil
}

// sameID compares UUID values rather than the caller's textual encoding.
func sameID(first, second string) bool {
	a, err := uuid.Parse(first)
	b, otherErr := uuid.Parse(second)
	return err == nil && otherErr == nil && a == b
}

func scanRoom(row pgx.Row) (*domain.Room, error) {
	var room domain.Room
	if err := row.Scan(roomFields(&room)...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scan room: %w", err)
	}
	return withParticipants(&room), nil
}

// roomFields scans roomColumns, temporarily storing both direct participants.
func roomFields(room *domain.Room, extra ...any) []any {
	room.ParticipantIDs = make([]string, 2)
	return append([]any{&room.ID, &room.Kind, &room.Name, &room.OwnerID, &room.Private, &room.CreatedAt,
		&room.ParticipantIDs[0], &room.ParticipantIDs[1]}, extra...)
}

func withParticipants(room *domain.Room) *domain.Room {
	if room.ParticipantIDs[0] == "" {
		room.ParticipantIDs = nil
	}
	return room
}

// CreateChannel atomically stores a channel and its owner's membership.
func (r *RoomRepository) CreateChannel(ctx context.Context, ownerID, name string, private bool) (*domain.Room, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin channel creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	room, err := scanRoom(tx.QueryRow(ctx, `INSERT INTO rooms (id, kind, name, owner_id, private)
  VALUES ($1::uuid, 'channel', $2, $3::uuid, $4) RETURNING `+roomColumns, uuid.NewString(), name, ownerID, private))
	if err != nil {
		return nil, fmt.Errorf("store channel: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO room_members (room_id, user_id) VALUES ($1::uuid, $2::uuid)`, room.ID, ownerID); err != nil {
		return nil, fmt.Errorf("enroll channel owner: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit channel creation: %w", err)
	}
	return room, nil
}
