package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/validator"
)

// ErrInvalidAttachment rejects absent, foreign or duplicate references.
var ErrInvalidAttachment = errors.New("chat: invalid attachment")

// ErrMediaUnavailable indicates file verification cannot currently complete.
var ErrMediaUnavailable = errors.New("chat: media unavailable")

// AttachmentChecker verifies membership, room and uploader through Media.
type AttachmentChecker interface {
	CheckAttachment(context.Context, string, string, string) error
}

// MessageOption configures optional message capabilities.
type MessageOption func(*MessageService)

// WithAttachmentChecker enables file messages without changing text-only sends.
func WithAttachmentChecker(check AttachmentChecker) MessageOption {
	return func(s *MessageService) { s.attachments = check }
}

type attachmentRepository interface {
	SendMessageWithAttachments(context.Context, string, string, string, string, []string) (*domain.Message, error)
}

// SendAttachments validates and checks references before taking the room write lock.
// Media calls back into Chat for membership, so verification must precede storage.
func (s *MessageService) SendAttachments(ctx context.Context, actorID, roomID, clientID, text string, ids []string) (*domain.Message, error) {
	if len(ids) == 0 {
		return s.SendMessage(ctx, actorID, roomID, clientID, text)
	}
	if len(ids) > 5 {
		return nil, ErrInvalidAttachment
	}
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
	if text != "" {
		if err := validator.MessageText(text); err != nil {
			return nil, err
		}
	}
	canonical := make([]string, len(ids))
	seen := make(map[string]bool, len(ids))
	for i, id := range ids {
		value, err := validator.CanonicalID(id)
		if err != nil || seen[value] {
			return nil, ErrInvalidAttachment
		}
		seen[value] = true
		canonical[i] = value
	}
	repo, ok := s.messages.(attachmentRepository)
	if !ok || s.attachments == nil {
		return nil, ErrMediaUnavailable
	}
	for _, id := range canonical {
		if err := s.attachments.CheckAttachment(ctx, actor, room, id); err != nil {
			return nil, fmt.Errorf("verify attachment: %w", err)
		}
	}
	message, err := repo.SendMessageWithAttachments(ctx, room, actor, client, text, canonical)
	if err != nil {
		return nil, fmt.Errorf("send attachments: %w", err)
	}
	return message, nil
}
