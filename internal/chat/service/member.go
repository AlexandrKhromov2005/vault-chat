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

// RemoveMember asks storage to atomically verify ownership and remove another
// member. The target is not resolved through Auth: removal must also work for
// identities that no longer exist, and removing a nonmember is a no-op.
func (s *Service) RemoveMember(ctx context.Context, actorID, roomID, userID string) error {
	actor, err := validator.CanonicalID(actorID)
	if err != nil {
		return fmt.Errorf("removal actor: %w", err)
	}
	id, err := validator.CanonicalID(roomID)
	if err != nil {
		return fmt.Errorf("removal room: %w", err)
	}
	user, err := validator.CanonicalID(userID)
	if err != nil {
		return fmt.Errorf("removal user: %w", err)
	}
	if err := s.rooms.RemoveChannelMember(ctx, id, actor, user); err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	return nil
}

// LeaveRoom removes the actor's own channel membership.
func (s *Service) LeaveRoom(ctx context.Context, actorID, roomID string) error {
	actor, err := validator.CanonicalID(actorID)
	if err != nil {
		return fmt.Errorf("leave actor: %w", err)
	}
	id, err := validator.CanonicalID(roomID)
	if err != nil {
		return fmt.Errorf("leave room: %w", err)
	}
	if err := s.rooms.LeaveRoom(ctx, id, actor); err != nil {
		return fmt.Errorf("leave room: %w", err)
	}
	return nil
}
