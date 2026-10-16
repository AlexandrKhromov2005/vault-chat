package domain

import (
	"errors"
	"time"
)

// ErrMessageConflict rejects reuse of an idempotency key with different text.
var ErrMessageConflict = errors.New("chat: message key already used")

// Message is immutable. Sequence orders committed messages within one room.
type Message struct {
	ID, RoomID, SenderID, ClientID, Text string
	AttachmentIDs                        []string
	Sequence                             int64
	CreatedAt                            time.Time
}
