package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/validator"
)

var (
	// ErrInvalidLimit rejects history requests outside the supported page size.
	ErrInvalidLimit = errors.New("chat: history limit must be 1–100")
	// ErrCatchUpWithCursor rejects a request for older history and catch-up at once.
	ErrCatchUpWithCursor = errors.New("chat: cursor and after are mutually exclusive")
)

// MessageRepository must authorize each read/write atomically with membership.
type MessageRepository interface {
	SendMessage(context.Context, string, string, string, string) (*domain.Message, error)
	ListMessages(context.Context, string, string, int64, int) ([]*domain.Message, error)
	ListMessagesAfter(context.Context, string, string, int64, int) ([]*domain.Message, error)
}

// MessageService validates immutable text messages for authenticated actors.
type MessageService struct {
	messages    MessageRepository
	attachments AttachmentChecker
}

// NewMessageService binds member-authorized message storage.
func NewMessageService(messages MessageRepository, options ...MessageOption) *MessageService {
	s := &MessageService{messages: messages}
	for _, option := range options {
		option(s)
	}
	return s
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

// MessagePage contains one page of messages and the exclusive position of the
// next page in the same direction; it is empty after the last page.
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
	limit, err = pageSize(limit, ErrInvalidLimit)
	if err != nil {
		return nil, err
	}
	messages, err := s.messages.ListMessages(ctx, room, actor, before, limit+1)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	return messagePage(messages, limit), nil
}

// History reads one page from a single position: older history before cursor,
// or, when after is set, catch-up after it. A request with both is rejected
// because the positions select opposite directions.
func (s *MessageService) History(ctx context.Context, actorID, roomID, cursor, after string, limit int) (*MessagePage, error) {
	switch {
	case after != "" && cursor != "":
		return nil, ErrCatchUpWithCursor
	case after != "":
		return s.CatchUp(ctx, actorID, roomID, after, limit)
	default:
		return s.ListMessages(ctx, actorID, roomID, cursor, limit)
	}
}

// CatchUp returns messages after a room position, oldest first, so a client can
// fetch what it missed while disconnected without paging through newer history.
func (s *MessageService) CatchUp(ctx context.Context, actorID, roomID, after string, limit int) (*MessagePage, error) {
	actor, err := validator.CanonicalID(actorID)
	if err != nil {
		return nil, err
	}
	room, err := validator.CanonicalID(roomID)
	if err != nil {
		return nil, err
	}
	position, err := validator.CatchUpCursor(after)
	if err != nil {
		return nil, err
	}
	limit, err = pageSize(limit, ErrInvalidLimit)
	if err != nil {
		return nil, err
	}
	messages, err := s.messages.ListMessagesAfter(ctx, room, actor, position, limit+1)
	if err != nil {
		return nil, fmt.Errorf("catch up messages: %w", err)
	}
	return messagePage(messages, limit), nil
}

// messagePage trims the lookahead row; its presence yields the next position.
func messagePage(messages []*domain.Message, limit int) *MessagePage {
	page := &MessagePage{Messages: messages}
	if len(messages) > limit {
		page.Messages = messages[:limit]
		page.NextCursor = validator.FormatHistoryCursor(messages[limit-1].Sequence)
	}
	return page
}
