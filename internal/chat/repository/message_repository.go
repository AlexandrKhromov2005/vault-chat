package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
)

const messageColumns = `id::text, room_id::text, sender_id::text, client_id::text, text, sequence, created_at`

// SendMessage serializes writes within a room so a committed cursor cannot skip
// an earlier, still-uncommitted send. Retries never advance the room sequence.
func (r *RoomRepository) SendMessage(ctx context.Context, roomID, actorID, clientID, text string) (*domain.Message, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin message: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var sequence int64
	err = tx.QueryRow(ctx, `SELECT message_sequence FROM rooms WHERE id=$1::uuid
 AND EXISTS (SELECT 1 FROM room_members WHERE room_id=rooms.id AND user_id=$2::uuid)
 FOR UPDATE`, roomID, actorID).Scan(&sequence)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("authorize message: %w", err)
	}
	message, err := scanMessage(tx.QueryRow(ctx, `SELECT `+messageColumns+` FROM messages
 WHERE room_id=$1::uuid AND sender_id=$2::uuid AND client_id=$3::uuid`, roomID, actorID, clientID))
	if err == nil {
		if message.Text != text {
			return nil, domain.ErrMessageConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit message retry: %w", err)
		}
		return message, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("find message retry: %w", err)
	}
	// PostgreSQL checks overflow; the transaction rolls back on either write failing.
	err = tx.QueryRow(ctx, `UPDATE rooms SET message_sequence=message_sequence+1
 WHERE id=$1::uuid RETURNING message_sequence`, roomID).Scan(&sequence)
	if err != nil {
		return nil, fmt.Errorf("advance message sequence: %w", err)
	}
	message, err = scanMessage(tx.QueryRow(ctx, `INSERT INTO messages (id, room_id, sender_id, client_id, text, sequence)
 VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6) RETURNING `+messageColumns,
		uuid.NewString(), roomID, actorID, clientID, text, sequence))
	if err != nil {
		return nil, fmt.Errorf("store message: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit message: %w", err)
	}
	return message, nil
}

// ListMessages returns newest first with an exclusive room-local cursor.
// Membership and the page use one snapshot, including for an empty room.
func (r *RoomRepository) ListMessages(ctx context.Context, roomID, actorID string, before int64, limit int) ([]*domain.Message, error) {
	rows, err := r.pool.Query(ctx, `SELECT m.id::text, m.room_id::text, m.sender_id::text,
 m.client_id::text, m.text, m.sequence, m.created_at FROM rooms r
 LEFT JOIN LATERAL (SELECT * FROM messages WHERE room_id=r.id AND ($3::bigint=0 OR sequence<$3)
 ORDER BY sequence DESC LIMIT $4) m ON true
 WHERE r.id=$1::uuid AND EXISTS (SELECT 1 FROM room_members WHERE room_id=r.id AND user_id=$2::uuid)
 ORDER BY m.sequence DESC`, roomID, actorID, before, limit)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()
	messages := make([]*domain.Message, 0)
	authorized := false
	for rows.Next() {
		authorized = true
		var id, room, sender, client, text *string
		var sequence *int64
		// The left join has one null row for an authorized empty page.
		message := &domain.Message{}
		var timestamp *time.Time
		if err := rows.Scan(&id, &room, &sender, &client, &text, &sequence, &timestamp); err != nil {
			return nil, fmt.Errorf("scan history: %w", err)
		}
		if id != nil {
			*message = domain.Message{ID: *id, RoomID: *room, SenderID: *sender, ClientID: *client, Text: *text, Sequence: *sequence, CreatedAt: *timestamp}
			messages = append(messages, message)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read messages: %w", err)
	}
	if !authorized {
		return nil, domain.ErrNotFound
	}
	return messages, nil
}

func scanMessage(row pgx.Row) (*domain.Message, error) {
	message := &domain.Message{}
	if err := row.Scan(&message.ID, &message.RoomID, &message.SenderID, &message.ClientID, &message.Text, &message.Sequence, &message.CreatedAt); err != nil {
		return nil, err
	}
	return message, nil
}
