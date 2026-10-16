package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/validator"
)

// ErrInvalidMemberLimit rejects member list requests outside the supported page size.
var ErrInvalidMemberLimit = errors.New("chat: member list limit must be 1–100")

// MemberPage contains members in user ID order and an exclusive next-page
// cursor; the cursor is empty after the last page.
type MemberPage struct {
	Members    []*domain.Member
	NextCursor string
}

// ListMembers returns the roster only to a member of the room.
func (s *Service) ListMembers(ctx context.Context, actorID, roomID, cursor string, limit int) (*MemberPage, error) {
	actor, err := validator.CanonicalID(actorID)
	if err != nil {
		return nil, fmt.Errorf("member list actor: %w", err)
	}
	id, err := validator.CanonicalID(roomID)
	if err != nil {
		return nil, fmt.Errorf("member list room: %w", err)
	}
	after, err := validator.MemberCursor(cursor)
	if err != nil {
		return nil, err
	}
	limit, err = pageSize(limit, ErrInvalidMemberLimit)
	if err != nil {
		return nil, err
	}
	members, err := s.rooms.ListMembers(ctx, id, actor, after, limit+1)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	page := &MemberPage{Members: members}
	if len(members) > limit {
		page.Members = members[:limit]
		page.NextCursor = members[limit-1].UserID
	}
	return page, nil
}
