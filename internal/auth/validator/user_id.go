package validator

import (
	"errors"

	"github.com/google/uuid"
)

// ErrInvalidUserID means a directory identifier is not a nonzero UUID.
var ErrInvalidUserID = errors.New("invalid user identifier")

// CanonicalUserID normalizes supported UUID encodings before account lookup.
func CanonicalUserID(input string) (string, error) {
	id, err := uuid.Parse(input)
	if err != nil || id == uuid.Nil {
		return "", ErrInvalidUserID
	}
	return id.String(), nil
}
