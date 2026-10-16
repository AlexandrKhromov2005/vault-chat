package validator

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
)

var (
	// ErrInvalidRoomCursor rejects noncanonical room list positions.
	ErrInvalidRoomCursor = errors.New("chat: invalid room list cursor")
	// ErrInvalidMemberCursor rejects noncanonical member list positions.
	ErrInvalidMemberCursor = errors.New("chat: invalid member list cursor")
)

// maxActivityMicros is 9999-12-31T23:59:59.999999Z, the last RFC 3339 instant.
const maxActivityMicros = 253402300799999999

// RoomCursor accepts an empty first-page cursor or exactly the text produced by
// FormatRoomCursor: non-negative Unix microseconds, "_" and a canonical room UUID.
func RoomCursor(input string) (domain.RoomPosition, error) {
	if input == "" {
		return domain.RoomPosition{}, nil
	}
	micros, id, _ := strings.Cut(input, "_")
	value, err := strconv.ParseInt(micros, 10, 64)
	if err != nil || value < 0 || value > maxActivityMicros {
		return domain.RoomPosition{}, ErrInvalidRoomCursor
	}
	position := domain.RoomPosition{Activity: time.UnixMicro(value).UTC(), ID: id}
	if canonical, err := CanonicalID(id); err != nil || canonical != id || FormatRoomCursor(position) != input {
		return domain.RoomPosition{}, ErrInvalidRoomCursor
	}
	return position, nil
}

// FormatRoomCursor produces the canonical position after the given room.
func FormatRoomCursor(position domain.RoomPosition) string {
	return strconv.FormatInt(position.Activity.UnixMicro(), 10) + "_" + position.ID
}

// MemberCursor accepts an empty first-page cursor or the canonical UUID of the
// last member of the previous page; other spellings would alias one position.
func MemberCursor(input string) (string, error) {
	if input == "" {
		return "", nil
	}
	if id, err := CanonicalID(input); err != nil || id != input {
		return "", ErrInvalidMemberCursor
	}
	return input, nil
}
