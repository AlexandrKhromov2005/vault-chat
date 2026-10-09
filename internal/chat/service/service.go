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

// CreateChannel creates a channel whose owner is the authenticated actor.
func (s *Service) CreateChannel(ctx context.Context, actorID, name string, private bool) (*domain.Room, error) {
	actor, err := validator.CanonicalID(actorID)
	if err != nil {
		return nil, fmt.Errorf("channel owner: %w", err)
	}
	if err := validator.ChannelName(name); err != nil {
		return nil, err
	}
	room, err := s.rooms.CreateChannel(ctx, actor, name, private)
	if err != nil {
		return nil, fmt.Errorf("create channel: %w", err)
	}
	return room, nil
}

// GetRoom returns metadata only when the actor belongs to the room.
func (s *Service) GetRoom(ctx context.Context, actorID, roomID string) (*domain.Room, error) {
	actor, err := validator.CanonicalID(actorID)
	if err != nil {
		return nil, fmt.Errorf("room actor: %w", err)
	}
	id, err := validator.CanonicalID(roomID)
	if err != nil {
		return nil, fmt.Errorf("room identifier: %w", err)
	}
	room, err := s.rooms.GetForMember(ctx, id, actor)
	if err != nil {
		return nil, fmt.Errorf("get room: %w", err)
	}
	return room, nil
}

// AddMember asks storage to atomically verify the actor's owner rights and add
// the resolved target identity. Direct room participants cannot be changed.
func (s *Service) AddMember(ctx context.Context, actorID, roomID, userID string) error {
	actor, err := validator.CanonicalID(actorID)
	if err != nil {
		return fmt.Errorf("member actor: %w", err)
	}
	id, err := validator.CanonicalID(roomID)
	if err != nil {
		return fmt.Errorf("member room: %w", err)
	}
	user, err := validator.CanonicalID(userID)
	if err != nil {
		return fmt.Errorf("member user: %w", err)
	}
	if err := s.rooms.AddChannelMember(ctx, id, actor, user); err != nil {
		return fmt.Errorf("add member: %w", err)
	}
	return nil
}
