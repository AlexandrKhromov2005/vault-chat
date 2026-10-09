// Package validator validates identifiers and room inputs for Chat.
package validator

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	// ErrInvalidID means the input is not a nonzero UUID.
	ErrInvalidID = errors.New("chat: invalid identifier")
	// ErrInvalidName means a channel name violates the display name limits.
	ErrInvalidName = errors.New("chat: invalid channel name")
)

// CanonicalID accepts UUID representations supported by google/uuid and returns
// their canonical form. Comparing raw strings could allow a self-dialog under
// differently formatted identifiers. UUID syntax does not prove a user exists.
func CanonicalID(input string) (string, error) {
	id, err := uuid.Parse(input)
	if err != nil || id == uuid.Nil {
		return "", ErrInvalidID
	}
	return id.String(), nil
}

// ChannelName requires 1–80 Unicode code points, valid UTF-8, no control
// characters, and no leading or trailing whitespace. Names are not unique.
func ChannelName(name string) error {
	if !utf8.ValidString(name) || len(name) > 320 || utf8.RuneCountInString(name) < 1 ||
		utf8.RuneCountInString(name) > 80 || strings.TrimSpace(name) != name {
		return ErrInvalidName
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return ErrInvalidName
		}
	}
	return nil
}
