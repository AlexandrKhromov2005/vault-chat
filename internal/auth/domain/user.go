// Package domain contains the core business entities of the auth service.
package domain

import "time"

// User is a registered account in the auth service.
type User struct {
	ID           string
	Email        string
	Username     string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
