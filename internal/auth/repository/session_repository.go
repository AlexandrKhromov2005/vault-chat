package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
)

var (
	// ErrSessionRevoked is returned when an operation requires an active
	// session but the session was revoked (logout or revoke-all).
	ErrSessionRevoked = errors.New("repository: session revoked")
	// ErrSessionReused is returned when a session that was already replaced by
	// rotation is rotated again, i.e. its refresh token was used twice.
	ErrSessionReused = errors.New("repository: rotated session reused")
)

// SessionRepository persists login sessions in PostgreSQL.
type SessionRepository struct {
	pool           *pgxpool.Pool
	legacyDeadline *time.Time
}

// NewSessionRepository creates a repository on top of the given connection pool.
func NewSessionRepository(pool *pgxpool.Pool, deadline ...*time.Time) *SessionRepository {
	r := &SessionRepository{pool: pool}
	if len(deadline) > 0 {
		r.legacyDeadline = deadline[0]
	}
	return r
}

// activeSession is the rule for a session row "current", joined with its
// user "u", that keeps its login active; deadline is the parameter holding
// the legacy email verification deadline.
func activeSession(deadline string) string {
	return `current.revoked_at IS NULL AND current.expires_at>clock_timestamp()
AND current.credential_version=u.credential_version
AND (u.email_verified_at IS NOT NULL OR (NOT u.email_verification_required
AND (` + deadline + `::timestamptz IS NULL OR clock_timestamp()<` + deadline + `)))`
}

// rowQuerier is a pool or a transaction.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// IsActive checks the login family, so refresh rotation does not invalidate
// requests already using the previous access token. Logout/reuse revoke it.
func (r *SessionRepository) IsActive(ctx context.Context, userID, sessionID string) (bool, error) {
	return r.isActive(ctx, r.pool, userID, sessionID)
}

func (r *SessionRepository) isActive(ctx context.Context, q rowQuerier, userID, sessionID string) (bool, error) {
	var active bool
	err := q.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM sessions original JOIN sessions current ON current.family_id=original.family_id
JOIN users u ON u.id=current.user_id
WHERE original.id=$1::uuid AND original.user_id=$2::uuid AND current.user_id=$2::uuid
AND `+activeSession("$3")+`)`, sessionID, userID, r.legacyDeadline).Scan(&active)
	return active, err
}

// Create stores a new active session that starts a session family.
func (r *SessionRepository) Create(ctx context.Context, session *domain.Session) error {
	return r.withUserLock(ctx, session.UserID, func(tx pgx.Tx) error {
		if err := r.sessionUserCurrent(ctx, tx, session); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO sessions (id,user_id,family_id,expires_at,credential_version)
VALUES ($1::uuid,$2::uuid,$1::uuid,$3,$4)`, session.ID, session.UserID, session.ExpiresAt, session.CredentialVersion)
		return err
	})
}

func (r *SessionRepository) sessionUserCurrent(ctx context.Context, tx pgx.Tx, session *domain.Session) error {
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT credential_version=$2 AND
(email_verified_at IS NOT NULL OR (NOT email_verification_required AND
($3::timestamptz IS NULL OR clock_timestamp()<$3))) FROM users WHERE id=$1::uuid`,
		session.UserID, session.CredentialVersion, r.legacyDeadline).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !allowed {
		return ErrSessionRevoked
	}
	return nil
}

// Rotate atomically revokes the active session currentID of next.UserID and
// stores next in its place, in the same family. It returns ErrSessionReused if
// currentID was already rotated, ErrSessionRevoked if it was revoked otherwise,
// and ErrNotFound if the user has no such session or it has expired.
func (r *SessionRepository) Rotate(ctx context.Context, currentID string, next *domain.Session) error {
	return r.withUserLock(ctx, next.UserID, func(tx pgx.Tx) error {
		if err := r.sessionUserCurrent(ctx, tx, next); err != nil {
			return err
		}
		const revoke = `UPDATE sessions SET revoked_at = now(), replaced_by = $3::uuid
			WHERE id = $1::uuid AND user_id = $2::uuid AND credential_version=$4
			AND revoked_at IS NULL AND expires_at > clock_timestamp()
			RETURNING family_id`
		var familyID string
		err := tx.QueryRow(ctx, revoke, currentID, next.UserID, next.ID, next.CredentialVersion).Scan(&familyID)
		if errors.Is(err, pgx.ErrNoRows) {
			return inactiveSessionError(ctx, tx, currentID, next.UserID, next.CredentialVersion)
		}
		if err != nil {
			return fmt.Errorf("failed to revoke session: %w", err)
		}

		const insert = `INSERT INTO sessions (id, user_id, family_id, expires_at,credential_version)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4,$5)`
		if _, err := tx.Exec(ctx, insert, next.ID, next.UserID, familyID, next.ExpiresAt, next.CredentialVersion); err != nil {
			return fmt.Errorf("failed to insert session: %w", err)
		}
		return nil
	})
}

// RevokeFamily revokes the session sessionID of userID together with every
// session that descends from the same login, so that logging out with a
// token that was already rotated still ends the login. Revoking an unknown or
// already revoked session is a no-op.
func (r *SessionRepository) RevokeFamily(ctx context.Context, userID, sessionID string) error {
	return r.withUserLock(ctx, userID, func(tx pgx.Tx) error {
		const query = `UPDATE sessions SET revoked_at = now()
			WHERE family_id = (SELECT family_id FROM sessions WHERE id = $2::uuid AND user_id = $1::uuid)
			AND revoked_at IS NULL`

		if _, err := tx.Exec(ctx, query, userID, sessionID); err != nil {
			return fmt.Errorf("failed to revoke session family: %w", err)
		}
		return nil
	})
}

// RevokeAllForUser revokes every active session of the user and returns how
// many sessions were revoked.
func (r *SessionRepository) RevokeAllForUser(ctx context.Context, userID string) (int64, error) {
	var revoked int64
	err := r.withUserLock(ctx, userID, func(tx pgx.Tx) error {
		const query = `UPDATE sessions SET revoked_at = now()
			WHERE user_id = $1::uuid AND revoked_at IS NULL`

		tag, err := tx.Exec(ctx, query, userID)
		if err != nil {
			return fmt.Errorf("failed to revoke user sessions: %w", err)
		}
		revoked = tag.RowsAffected()
		return nil
	})
	return revoked, err
}

// withUserLock runs fn in a transaction holding a lock on the sessions of
// userID. Row locks alone are not enough: a revoking statement started while
// a rotation is in flight cannot see the session the rotation inserts, so the
// new session would survive. Serializing every revoking transaction of a user
// guarantees each one sees the outcome of the previous one. The lock key is
// derived from the canonical form of the uuid, so every spelling of userID
// takes the same lock.
func (r *SessionRepository) withUserLock(ctx context.Context, userID string, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const lock = `SELECT pg_advisory_xact_lock(hashtextextended('auth.sessions:' || ($1::uuid)::text, 0))`
	if _, err := tx.Exec(ctx, lock, userID); err != nil {
		return fmt.Errorf("failed to lock user sessions: %w", err)
	}

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// inactiveSessionError explains why the session id of userID could not be
// rotated.
func inactiveSessionError(ctx context.Context, tx pgx.Tx, id, userID string, version int64) error {
	var revoked, rotated bool
	var storedVersion int64
	err := tx.QueryRow(ctx,
		`SELECT revoked_at IS NOT NULL, replaced_by IS NOT NULL, credential_version FROM sessions
		WHERE id = $1::uuid AND user_id = $2::uuid`,
		id, userID).Scan(&revoked, &rotated, &storedVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to load session: %w", err)
	}
	if storedVersion != version {
		return ErrSessionRevoked
	}
	switch {
	case rotated:
		return ErrSessionReused
	case revoked:
		return ErrSessionRevoked
	default:
		return ErrNotFound // exists, not revoked, therefore expired
	}
}
