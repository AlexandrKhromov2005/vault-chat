package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// MaxPushDevicesPerUser bounds the tokens of one user; registering more drops
// the least recently registered ones.
const MaxPushDevicesPerUser = 10

// ErrPushTokenConflict is returned when a concurrent registration took the
// same token; the caller may retry.
var ErrPushTokenConflict = errors.New("repository: push token registered concurrently")

// RegisterPushToken binds token to the login (session family) of sessionID.
// It holds the user's session lock, which every revocation takes as well, and
// checks the login with the access-token rule under it, so a token is never
// stored for a revoked login. The login's previous token is replaced and the
// token leaves any other login. Tokens of logins that ended without a
// revocation are removed on the way. ErrSessionRevoked means the login is not
// active.
func (r *SessionRepository) RegisterPushToken(ctx context.Context, userID, sessionID, provider, token string) error {
	return r.withUserLock(ctx, userID, func(tx pgx.Tx) error {
		active, err := r.isActive(ctx, tx, userID, sessionID)
		if err != nil {
			return fmt.Errorf("check push session: %w", err)
		}
		if !active {
			return ErrSessionRevoked
		}
		var familyID string
		if err := tx.QueryRow(ctx, `SELECT family_id FROM sessions WHERE id=$1::uuid AND user_id=$2::uuid`,
			sessionID, userID).Scan(&familyID); err != nil {
			return fmt.Errorf("load push session family: %w", err)
		}
		prune := `DELETE FROM push_devices d WHERE d.user_id=$1::uuid AND NOT EXISTS (
SELECT 1 FROM sessions current JOIN users u ON u.id=current.user_id
WHERE current.family_id=d.family_id AND ` + activeSession("$2") + `)`
		if _, err := tx.Exec(ctx, prune, userID, r.legacyDeadline); err != nil {
			return fmt.Errorf("prune push tokens: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM push_devices WHERE token=$1 AND family_id<>$2::uuid`, token, familyID); err != nil {
			return fmt.Errorf("move push token: %w", err)
		}
		// clock_timestamp, not the transaction start: a registration that
		// waited for the lock is still the newest.
		_, err = tx.Exec(ctx, `INSERT INTO push_devices (family_id,user_id,provider,token,created_at,updated_at)
VALUES ($1::uuid,$2::uuid,$3,$4,clock_timestamp(),clock_timestamp())
ON CONFLICT (family_id) DO UPDATE SET provider=excluded.provider,token=excluded.token,updated_at=clock_timestamp()`,
			familyID, userID, provider, token)
		// A concurrent registration of the same token by another user takes
		// the unique slot or deadlocks with this one.
		if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && (pgErr.Code == "23505" || pgErr.Code == "40P01") {
			return ErrPushTokenConflict
		}
		if err != nil {
			return fmt.Errorf("store push token: %w", err)
		}
		// The caller's own token always stays; the oldest others go.
		_, err = tx.Exec(ctx, `DELETE FROM push_devices WHERE family_id IN (SELECT family_id FROM push_devices
WHERE user_id=$1::uuid AND family_id<>$3::uuid ORDER BY updated_at DESC, family_id OFFSET $2)`,
			userID, MaxPushDevicesPerUser-1, familyID)
		if err != nil {
			return fmt.Errorf("bound push tokens: %w", err)
		}
		return nil
	})
}

// UnregisterPushToken removes the token of the login of sessionID, if any.
// It takes the user lock, so it orders exactly with registrations.
func (r *SessionRepository) UnregisterPushToken(ctx context.Context, userID, sessionID string) error {
	return r.withUserLock(ctx, userID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM push_devices WHERE user_id=$1::uuid AND family_id=(
SELECT family_id FROM sessions WHERE id=$2::uuid AND user_id=$1::uuid)`, userID, sessionID)
		if err != nil {
			return fmt.Errorf("delete push token: %w", err)
		}
		return nil
	})
}
