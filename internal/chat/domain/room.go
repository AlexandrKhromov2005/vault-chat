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
)

// Room is the stored room metadata. Direct rooms have empty Name and OwnerID;
// their participants have equal rights. Memberships are stored separately.
type Room struct {
	ID        string
	Kind      Kind
	Name      string
	OwnerID   string
	Private   bool
	CreatedAt time.Time
}
