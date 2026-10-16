package validator

import (
	"errors"
	"regexp"
)

const (
	defaultSearchLimit = 20
	maxSearchLimit     = 50
)

var (
	// ErrInvalidUsernameQuery means a search query is not a username prefix.
	ErrInvalidUsernameQuery = errors.New("invalid username query")
	// ErrInvalidSearchLimit means a search page size is out of range.
	ErrInvalidSearchLimit = errors.New("invalid search limit")
)

var usernameQueryPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{1,31}$`)

// ValidateUsernameQuery checks that query could start a valid username: an
// ASCII letter followed by 1 to 31 ASCII letters, digits, underscores, or
// dashes. Single-character queries are rejected because they match too much
// of the directory. Whitespace is rejected rather than trimmed.
func ValidateUsernameQuery(query string) error {
	if !usernameQueryPattern.MatchString(query) {
		return ErrInvalidUsernameQuery
	}
	return nil
}

// SearchLimit returns the page size for a search: 0 selects the default of
// 20, and anything outside 1-50 is rejected.
func SearchLimit(limit int32) (int, error) {
	switch {
	case limit == 0:
		return defaultSearchLimit, nil
	case limit < 0 || limit > maxSearchLimit:
		return 0, ErrInvalidSearchLimit
	}
	return int(limit), nil
}
