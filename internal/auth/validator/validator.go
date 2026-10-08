// Package validator provides input validation for the auth service.
package validator

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxEmailLength    = 254
	minUsernameLength = 3
	maxUsernameLength = 32
	minPasswordLength = 8
	maxPasswordLength = 128
)

var (
	// ErrInvalidEmail is returned when an email address fails validation.
	ErrInvalidEmail = errors.New("invalid email address")
	// ErrInvalidUsername is returned when a username fails validation.
	ErrInvalidUsername = errors.New("invalid username")
	// ErrInvalidPassword is returned when a password fails validation.
	ErrInvalidPassword = errors.New("invalid password")
)

var (
	emailPattern    = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)
	usernamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{2,31}$`)
)

// ValidateEmail checks that email is a syntactically valid address within
// the RFC 5321 length limit. It is intentionally syntax-only; deliverability
// is out of scope.
func ValidateEmail(email string) error {
	if len(email) == 0 || len(email) > maxEmailLength {
		return ErrInvalidEmail
	}
	if !utf8.ValidString(email) || strings.ContainsAny(email, " \t\r\n") {
		return ErrInvalidEmail
	}
	if !emailPattern.MatchString(email) {
		return ErrInvalidEmail
	}
	return nil
}

// ValidateUsername checks that username is 3-32 characters long, starts with
// an ASCII letter, and contains only ASCII letters, digits, underscores, and
// dashes.
func ValidateUsername(username string) error {
	if len(username) < minUsernameLength || len(username) > maxUsernameLength {
		return ErrInvalidUsername
	}
	if !usernamePattern.MatchString(username) {
		return ErrInvalidUsername
	}
	return nil
}

// ValidatePassword checks that password is 8-128 characters long and contains
// at least one ASCII letter and one ASCII digit.
func ValidatePassword(password string) error {
	if len(password) < minPasswordLength || len(password) > maxPasswordLength {
		return ErrInvalidPassword
	}

	var hasLetter, hasDigit bool
	for _, r := range password {
		switch {
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
			hasLetter = true
		case r >= '0' && r <= '9':
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return ErrInvalidPassword
	}
	return nil
}
