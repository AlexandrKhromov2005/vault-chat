// Package domain contains Chat-owned rooms and memberships. User identifiers
// reference identities owned by Auth; Chat never queries Auth's database.
package domain

import (
	"errors"
	"time"
)

// Kind distinguishes direct dialogs from channels.
type Kind string

const (
	// Direct is a private room for exactly two distinct identities.
	Direct Kind = "direct"
	// Channel is a room managed by its owner.
	Channel Kind = "channel"
)

var (
	// ErrNotFound hides both nonexistent rooms and rooms inaccessible to a user.
	ErrNotFound = errors.New("chat: room not found")
	// ErrForbidden denies a management operation to an existing room member.
	ErrForbidden = errors.New("chat: room operation forbidden")
	// ErrOwnerMembership keeps a channel owned while ownership cannot be transferred.
	ErrOwnerMembership = errors.New("chat: channel owner cannot leave or be removed")
)

// Room is the stored room metadata. Direct rooms have empty Name and OwnerID;
// their participants have equal rights. Channel memberships are stored separately.
type Room struct {
	ID        string
	Kind      Kind
	Name      string
	OwnerID   string
	Private   bool
	CreatedAt time.Time
	// ParticipantIDs holds both direct participants in UUID order; nil for channels.
	ParticipantIDs []string
}

// RoomSummary is a room in its member's list. Activity is the later of the last
// message and the member's enrollment; LastMessageAt is nil without messages.
type RoomSummary struct {
	Room
	LastMessageAt *time.Time
	Activity      time.Time
}

// RoomPosition is an exclusive keyset position in a member's room list, ordered
// by Activity and then ID, both descending. The zero value starts at the newest.
type RoomPosition struct {
	Activity time.Time
	ID       string
}

// Member is one membership of a room, as seen by another member.
type Member struct {
	UserID   string
	JoinedAt time.Time
}
