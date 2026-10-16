// Package domain contains Media's immutable file metadata.
package domain

import (
	"errors"
	"time"
)

// ErrNotFound hides both absent files and rooms inaccessible to the caller.
var ErrNotFound = errors.New("media: file not found")

// File is a room-scoped upload. The ID is also its private object key.
type File struct {
	ID          string
	RoomID      string
	OwnerID     string
	Name        string
	ContentType string
	Size        int64
	CreatedAt   time.Time
}
