// Package service provides Chat room operations for an authenticated transport.
package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/validator"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
)

// ErrSelfDialog prevents a dialog with two encodings of the same identity.
var ErrSelfDialog = errors.New("chat: direct participants must differ")

// RoomRepository is defined at its consumer. Implementations must atomically
// enroll initial members, enforce read membership and authorize channel writes.
type RoomRepository interface {
	GetOrCreateDirect(ctx context.Context, first, second string) (*domain.Room, error)
	CreateChannel(ctx context.Context, ownerID, name string, private bool) (*domain.Room, error)
	GetForMember(ctx context.Context, roomID, userID string) (*domain.Room, error)
	AddChannelMember(ctx context.Context, roomID, actorID, userID string) error
}

// Service validates inputs and delegates atomic access checks to storage.
// Actor IDs must come from a verified Auth identity. Target IDs must be resolved
// through Auth before calling methods that create memberships.
type Service struct{ rooms RoomRepository }

// NewService constructs the room service using its storage implementation.
func NewService(rooms RoomRepository) *Service { return &Service{rooms: rooms} }

// GetOrCreateDirect obtains one dialog for a pair of distinct trusted identities.
func (s *Service) GetOrCreateDirect(ctx context.Context, actorID, peerID string) (*domain.Room, error) {
	actor, err := validator.CanonicalID(actorID)
	if err != nil {
		return nil, fmt.Errorf("direct actor: %w", err)
	}
	peer, err := validator.CanonicalID(peerID)
	if err != nil {
		return nil, fmt.Errorf("direct peer: %w", err)
	}
	if actor == peer {
		return nil, ErrSelfDialog
	}
	room, err := s.rooms.GetOrCreateDirect(ctx, actor, peer)
	if err != nil {
		return nil, fmt.Errorf("create direct room: %w", err)
	}
	return room, nil
}
