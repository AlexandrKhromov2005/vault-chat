package domain

import "time"

// EmailAction binds a single-use proof to one account state and purpose.
type EmailAction struct {
	ID, UserID, Purpose, AccountEmail, TargetEmail string
	TokenHash                                      []byte
	CredentialVersion                              int64
	ExpiresAt                                      time.Time
}
