package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/validator"
)

// ErrInvalidRoomLimit rejects room list requests outside the supported page size.
var ErrInvalidRoomLimit = errors.New("chat: room list limit must be 1–100")

// RoomPage contains the actor's rooms, most recently active first, and an
// exclusive cursor of the next page; it is empty after the last page.
type RoomPage struct {
	Rooms      []*domain.RoomSummary
	NextCursor string
}

// ListRooms reads one more room than requested so the last page has no cursor.
func (s *Service) ListRooms(ctx context.Context, actorID, cursor string, limit int) (*RoomPage, error) {
	actor, err := validator.CanonicalID(actorID)
	if err != nil {
		return nil, fmt.Errorf("room list actor: %w", err)
	}
	after, err := validator.RoomCursor(cursor)
	if err != nil {
		return nil, err
	}
	limit, err = pageSize(limit, ErrInvalidRoomLimit)
	if err != nil {
		return nil, err
	}
	rooms, err := s.rooms.ListForMember(ctx, actor, after, limit+1)
	if err != nil {
		return nil, fmt.Errorf("list rooms: %w", err)
	}
	page := &RoomPage{Rooms: rooms}
	if len(rooms) > limit {
		last := rooms[limit-1]
		page.Rooms = rooms[:limit]
		page.NextCursor = validator.FormatRoomCursor(domain.RoomPosition{Activity: last.Activity, ID: last.ID})
	}
	return page, nil
}
