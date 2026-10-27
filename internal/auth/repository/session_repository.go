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
	pool *pgxpool.Pool
}

// NewSessionRepository creates a repository on top of the given connection pool.
func NewSessionRepository(pool *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{pool: pool}
}

// IsActive checks the login family, so refresh rotation does not invalidate
// requests already using the previous access token. Logout/reuse revoke it.
func (r *SessionRepository) IsActive(ctx context.Context, userID, sessionID string) (bool, error) {
	var active bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM sessions original JOIN sessions current ON current.family_id=original.family_id
WHERE original.id=$1::uuid AND original.user_id=$2::uuid AND current.user_id=$2::uuid
AND current.revoked_at IS NULL AND current.expires_at>now())`, sessionID, userID).Scan(&active)
	return active, err
}

// Create stores a new active session that starts a session family.
func (r *SessionRepository) Create(ctx context.Context, session *domain.Session) error {
	const query = `INSERT INTO sessions (id, user_id, family_id, expires_at)
		VALUES ($1::uuid, $2::uuid, $1::uuid, $3)`

	if _, err := r.pool.Exec(ctx, query, session.ID, session.UserID, session.ExpiresAt); err != nil {
		return fmt.Errorf("failed to insert session: %w", err)
	}
	return nil
}

// Rotate atomically revokes the active session currentID of next.UserID and
// stores next in its place, in the same family, and returns next.
//
// A retry of a rotation that the client did not see is told apart from token
// reuse: if currentID was rotated less than retryWindow ago into a session
// that is still active and was never rotated itself, nothing changes and that
// session is returned instead, so the caller can issue its tokens again. Only
// the latest replaced session qualifies, so a token is never accepted after
// the client has moved on with its successor.
//
// Otherwise Rotate returns ErrSessionReused if currentID was already rotated,
// ErrSessionRevoked if it was revoked otherwise, and ErrNotFound if the user
// has no such session or it has expired.
func (r *SessionRepository) Rotate(ctx context.Context, currentID string, next *domain.Session, retryWindow time.Duration) (*domain.Session, error) {
	active := next
	err := r.withUserLock(ctx, next.UserID, func(tx pgx.Tx) error {
		// Waiting for the user lock must not consume the new retry window.
		const revoke = `UPDATE sessions SET revoked_at = clock_timestamp(), replaced_by = $3::uuid
			WHERE id = $1::uuid AND user_id = $2::uuid AND revoked_at IS NULL AND expires_at > now()
			RETURNING family_id`
		var familyID string
		err := tx.QueryRow(ctx, revoke, currentID, next.UserID, next.ID).Scan(&familyID)
		if errors.Is(err, pgx.ErrNoRows) {
			active, err = retriedRotation(ctx, tx, currentID, next.UserID, retryWindow)
			return err
		}
		if err != nil {
			return fmt.Errorf("failed to revoke session: %w", err)
		}

		const insert = `INSERT INTO sessions (id, user_id, family_id, expires_at)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4)`
		if _, err := tx.Exec(ctx, insert, next.ID, next.UserID, familyID, next.ExpiresAt); err != nil {
			return fmt.Errorf("failed to insert session: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return active, nil
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

// retriedRotation returns the session that the inactive session id of userID
// was rotated into less than retryWindow ago, if that session is still active.
// Otherwise it explains why id could not be rotated.
func retriedRotation(ctx context.Context, tx pgx.Tx, id, userID string, retryWindow time.Duration) (*domain.Session, error) {
	// A rotated session keeps the time of its rotation in revoked_at: later
	// revocations only touch sessions that are still active. A successor
	// that is not revoked has not been rotated either.
	// now() is fixed at transaction start, before a potentially long lock wait;
	// evaluate both deadlines against the wall clock after taking the lock.
	const query = `SELECT current.revoked_at IS NOT NULL, current.replaced_by IS NOT NULL,
			successor.id::text, successor.expires_at
		FROM sessions current
		LEFT JOIN sessions successor ON successor.id = current.replaced_by
			AND successor.user_id = current.user_id
			AND current.revoked_at > clock_timestamp() - make_interval(secs => $3)
			AND successor.revoked_at IS NULL AND successor.expires_at > clock_timestamp()
		WHERE current.id = $1::uuid AND current.user_id = $2::uuid`
	var (
		revoked, rotated bool
		successorID      *string
		successorExpiry  *time.Time
	)
	err := tx.QueryRow(ctx, query, id, userID, retryWindow.Seconds()).
		Scan(&revoked, &rotated, &successorID, &successorExpiry)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load session: %w", err)
	}
	switch {
	case successorID != nil:
		return &domain.Session{ID: *successorID, UserID: userID, ExpiresAt: *successorExpiry}, nil
	case rotated:
		return nil, ErrSessionReused
	case revoked:
		return nil, ErrSessionRevoked
	default:
		return nil, ErrNotFound // exists, not revoked, therefore expired
	}
}
