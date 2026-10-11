package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/validator"
)

// ErrInvalidLimit rejects history requests outside the supported page size.
var ErrInvalidLimit = errors.New("chat: history limit must be 1–100")

// MessageRepository must authorize each read/write atomically with membership.
type MessageRepository interface {
	SendMessage(context.Context, string, string, string, string) (*domain.Message, error)
	ListMessages(context.Context, string, string, int64, int) ([]*domain.Message, error)
}

// MessageService validates immutable text messages for authenticated actors.
type MessageService struct{ messages MessageRepository }

// NewMessageService binds member-authorized message storage.
func NewMessageService(messages MessageRepository) *MessageService {
	return &MessageService{messages: messages}
}

// SendMessage canonicalizes the retry key and preserves the original text.
func (s *MessageService) SendMessage(ctx context.Context, actorID, roomID, clientID, text string) (*domain.Message, error) {
	actor, err := validator.CanonicalID(actorID)
	if err != nil {
		return nil, err
	}
	room, err := validator.CanonicalID(roomID)
	if err != nil {
		return nil, err
	}
	client, err := validator.CanonicalID(clientID)
	if err != nil {
		return nil, err
	}
	if err := validator.MessageText(text); err != nil {
		return nil, err
	}
	message, err := s.messages.SendMessage(ctx, room, actor, client, text)
	if err != nil {
		return nil, fmt.Errorf("send message: %w", err)
	}
	return message, nil
}

// MessagePage contains newest-first messages and an exclusive older-page cursor.
type MessagePage struct {
	Messages   []*domain.Message
	NextCursor string
}

// ListMessages uses a bounded lookahead so the last page has no next cursor.
func (s *MessageService) ListMessages(ctx context.Context, actorID, roomID, cursor string, limit int) (*MessagePage, error) {
	actor, err := validator.CanonicalID(actorID)
	if err != nil {
		return nil, err
	}
	room, err := validator.CanonicalID(roomID)
	if err != nil {
		return nil, err
	}
	before, err := validator.HistoryCursor(cursor)
	if err != nil {
		return nil, err
	}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return nil, ErrInvalidLimit
	}
	messages, err := s.messages.ListMessages(ctx, room, actor, before, limit+1)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	page := &MessagePage{Messages: messages}
	if len(messages) > limit {
		page.Messages = messages[:limit]
		page.NextCursor = validator.FormatHistoryCursor(messages[limit-1].Sequence)
	}
	return page, nil
}
