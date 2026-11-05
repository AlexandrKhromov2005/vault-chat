package validator

import (
	"errors"
	"regexp"
)

// MaxPushTokenLength bounds a provider registration token; FCM tokens are
// a few hundred characters. The unique index on tokens holds at most 2704
// bytes per row and random tokens do not compress, so the bound must stay below
// that; the check in migration 0007 repeats it.
const MaxPushTokenLength = 2048

// ErrInvalidPushToken rejects unknown providers and malformed tokens alike.
var ErrInvalidPushToken = errors.New("invalid push token")

var pushTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_:.-]+$`)

// ValidatePushToken accepts FCM registration tokens: 1 to 2048 characters of
// ASCII letters, digits and "_:.-". The database enforces the same rule.
func ValidatePushToken(provider, token string) error {
	if provider != "fcm" || len(token) > MaxPushTokenLength || !pushTokenPattern.MatchString(token) {
		return ErrInvalidPushToken
	}
	return nil
}
