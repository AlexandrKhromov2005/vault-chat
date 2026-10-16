package repository

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
)

const messageColumns = `id::text, room_id::text, sender_id::text, client_id::text, text, sequence, created_at, attachment_ids::text[]`

// SendMessage serializes writes within a room so a committed cursor cannot skip
// an earlier, still-uncommitted send. Retries never advance the room sequence
// and announce nothing; a new message is announced to the members at its commit.
func (r *RoomRepository) SendMessage(ctx context.Context, roomID, actorID, clientID, text string) (*domain.Message, error) {
	return r.sendMessage(ctx, roomID, actorID, clientID, text, nil)
}

// SendMessageWithAttachments stores verified file references atomically with text.
func (r *RoomRepository) SendMessageWithAttachments(ctx context.Context, roomID, actorID, clientID, text string, ids []string) (*domain.Message, error) {
	return r.sendMessage(ctx, roomID, actorID, clientID, text, ids)
}
func (r *RoomRepository) sendMessage(ctx context.Context, roomID, actorID, clientID, text string, ids []string) (*domain.Message, error) {
	if ids == nil {
		ids = []string{}
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin message: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := lockMemberRoom(ctx, tx, roomID, actorID); err != nil {
		return nil, fmt.Errorf("authorize message: %w", err)
	}
	message, err := scanMessage(tx.QueryRow(ctx, `SELECT `+messageColumns+` FROM messages
 WHERE room_id=$1::uuid AND sender_id=$2::uuid AND client_id=$3::uuid`, roomID, actorID, clientID))
	if err == nil {
		if message.Text != text || !slices.Equal(message.AttachmentIDs, ids) {
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
	var sequence int64
	err = tx.QueryRow(ctx, `UPDATE rooms SET message_sequence=message_sequence+1
 WHERE id=$1::uuid RETURNING message_sequence`, roomID).Scan(&sequence)
	if err != nil {
		return nil, fmt.Errorf("advance message sequence: %w", err)
	}
	message, err = scanMessage(tx.QueryRow(ctx, `INSERT INTO messages (id, room_id, sender_id, client_id, text, sequence, attachment_ids)
 VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7::uuid[]) RETURNING `+messageColumns,
		uuid.NewString(), roomID, actorID, clientID, text, sequence, ids))
	if err != nil {
		return nil, fmt.Errorf("store message: %w", err)
	}
	var events []domain.Event
	if r.notifier != nil {
		// The room lock is held, so membership cannot change before the commit.
		users, err := members(ctx, tx, message.RoomID)
		if err != nil {
			return nil, err
		}
		events = []domain.Event{{Kind: domain.MessageCreated, RoomID: message.RoomID, Message: message, Recipients: users}}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit message: %w", err)
	}
	r.notify(ctx, events...)
	if len(message.AttachmentIDs) == 0 {
		message.AttachmentIDs = nil
	}
	return message, nil
}

// ListMessages returns newest first with an exclusive room-local cursor.
// Membership and the page use one snapshot, including for an empty room.
func (r *RoomRepository) ListMessages(ctx context.Context, roomID, actorID string, before int64, limit int) ([]*domain.Message, error) {
	return r.memberMessages(ctx, `($3::bigint=0 OR sequence<$3) ORDER BY sequence DESC`, `DESC`, roomID, actorID, before, limit)
}

// ListMessagesAfter returns messages with a sequence above after, oldest first.
// Membership and the page use one snapshot, including for an empty page.
func (r *RoomRepository) ListMessagesAfter(ctx context.Context, roomID, actorID string, after int64, limit int) ([]*domain.Message, error) {
	return r.memberMessages(ctx, `sequence>$3 ORDER BY sequence`, `ASC`, roomID, actorID, after, limit)
}

// memberMessages reads one page selected and ordered by page, a constant SQL
// fragment over $3, only if the actor is a member of the room.
func (r *RoomRepository) memberMessages(ctx context.Context, page, order, roomID, actorID string, position int64, limit int) ([]*domain.Message, error) {
	rows, err := r.pool.Query(ctx, `SELECT m.id::text, m.room_id::text, m.sender_id::text,
 m.client_id::text, m.text, m.sequence, m.created_at, m.attachment_ids::text[] FROM rooms r
 LEFT JOIN LATERAL (SELECT * FROM messages WHERE room_id=r.id AND `+page+` LIMIT $4) m ON true
 WHERE r.id=$1::uuid AND EXISTS (SELECT 1 FROM room_members WHERE room_id=r.id AND user_id=$2::uuid)
 ORDER BY m.sequence `+order, roomID, actorID, position, limit)
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
		var attachments []string
		if err := rows.Scan(&id, &room, &sender, &client, &text, &sequence, &timestamp, &attachments); err != nil {
			return nil, fmt.Errorf("scan history: %w", err)
		}
		if len(attachments) == 0 {
			attachments = nil
		}
		if id != nil {
			*message = domain.Message{ID: *id, RoomID: *room, SenderID: *sender, ClientID: *client, Text: *text, Sequence: *sequence, CreatedAt: *timestamp, AttachmentIDs: attachments}
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
	if err := row.Scan(&message.ID, &message.RoomID, &message.SenderID, &message.ClientID, &message.Text, &message.Sequence, &message.CreatedAt, &message.AttachmentIDs); err != nil {
		return nil, err
	}
	if len(message.AttachmentIDs) == 0 {
		message.AttachmentIDs = nil
	}
	return message, nil
}
