package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// Create stores a new active session.
func (r *SessionRepository) Create(ctx context.Context, session *domain.Session) error {
	return insertSession(ctx, r.pool, session)
}

// Rotate atomically revokes the active session currentID and stores next in
// its place. It returns ErrSessionReused if currentID was already rotated,
// ErrSessionRevoked if it was revoked otherwise, and ErrNotFound if it does
// not exist or has expired.
func (r *SessionRepository) Rotate(ctx context.Context, currentID string, next *domain.Session) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin session rotation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The row lock taken by UPDATE serializes concurrent rotations of the same
	// session: the loser re-evaluates the WHERE clause and sees it revoked.
	const revoke = `UPDATE sessions SET revoked_at = now(), replaced_by = $2::uuid
		WHERE id = $1::uuid AND revoked_at IS NULL AND expires_at > now()`
	tag, err := tx.Exec(ctx, revoke, currentID, next.ID)
	if err != nil {
		return fmt.Errorf("failed to revoke session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return inactiveSessionError(ctx, tx, currentID)
	}

	if err := insertSession(ctx, tx, next); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit session rotation: %w", err)
	}
	return nil
}

// Revoke revokes the session with the given id. Revoking an unknown or
// already revoked session is a no-op.
func (r *SessionRepository) Revoke(ctx context.Context, id string) error {
	const query = `UPDATE sessions SET revoked_at = now()
		WHERE id = $1::uuid AND revoked_at IS NULL`

	if _, err := r.pool.Exec(ctx, query, id); err != nil {
		return fmt.Errorf("failed to revoke session: %w", err)
	}
	return nil
}

// RevokeAllForUser revokes every active session of the user and returns how
// many sessions were revoked.
func (r *SessionRepository) RevokeAllForUser(ctx context.Context, userID string) (int64, error) {
	const query = `UPDATE sessions SET revoked_at = now()
		WHERE user_id = $1::uuid AND revoked_at IS NULL`

	tag, err := r.pool.Exec(ctx, query, userID)
	if err != nil {
		return 0, fmt.Errorf("failed to revoke user sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// execer is satisfied by both *pgxpool.Pool and pgx.Tx.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func insertSession(ctx context.Context, db execer, session *domain.Session) error {
	const query = `INSERT INTO sessions (id, user_id, expires_at)
		VALUES ($1::uuid, $2::uuid, $3)`

	if _, err := db.Exec(ctx, query, session.ID, session.UserID, session.ExpiresAt); err != nil {
		return fmt.Errorf("failed to insert session: %w", err)
	}
	return nil
}

// inactiveSessionError explains why a session could not be rotated.
func inactiveSessionError(ctx context.Context, tx pgx.Tx, id string) error {
	var revoked, rotated bool
	err := tx.QueryRow(ctx,
		"SELECT revoked_at IS NOT NULL, replaced_by IS NOT NULL FROM sessions WHERE id = $1::uuid",
		id).Scan(&revoked, &rotated)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to load session: %w", err)
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
