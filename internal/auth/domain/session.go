package domain

import "time"

// Session is a server-side login session. Each refresh token is bound to
// exactly one session via its jti claim; revoking the session invalidates the
// token.
type Session struct {
	ID        string
	UserID    string
	ExpiresAt time.Time
}
