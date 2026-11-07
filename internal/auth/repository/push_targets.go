package repository

import (
	"context"
	"fmt"
)

// PushTargets returns the tokens of userID's active logins, with the
// access-token rule, so a login that ended without a revocation (expiry,
// credential change, verification deadline) gets nothing.
func (r *SessionRepository) PushTargets(ctx context.Context, userID string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT d.token FROM push_devices d WHERE d.user_id=$1::uuid AND EXISTS (
SELECT 1 FROM sessions current JOIN users u ON u.id=current.user_id
WHERE current.family_id=d.family_id AND current.user_id=$1::uuid AND `+activeSession("$2")+`)
ORDER BY d.updated_at DESC LIMIT $3`, userID, r.legacyDeadline, MaxPushDevicesPerUser)
	if err != nil {
		return nil, fmt.Errorf("load push targets: %w", err)
	}
	defer rows.Close()
	var tokens []string
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			return nil, fmt.Errorf("scan push target: %w", err)
		}
		tokens = append(tokens, token)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load push targets: %w", err)
	}
	return tokens, nil
}

// IsPushTarget reports whether token belongs to an active login of userID,
// with the access-token rule. The dispatcher calls it right before each
// provider request, so a revocation or a move of the token to another login
// committed before the call stops the request.
func (r *SessionRepository) IsPushTarget(ctx context.Context, userID, token string) (bool, error) {
	var active bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM push_devices d WHERE d.user_id=$1::uuid AND d.token=$2 AND EXISTS (
SELECT 1 FROM sessions current JOIN users u ON u.id=current.user_id
WHERE current.family_id=d.family_id AND current.user_id=$1::uuid AND `+activeSession("$3")+`))`,
		userID, token, r.legacyDeadline).Scan(&active)
	if err != nil {
		return false, fmt.Errorf("check push target: %w", err)
	}
	return active, nil
}

// DeletePushToken removes a token the provider no longer accepts, whichever
// login holds it now.
func (r *SessionRepository) DeletePushToken(ctx context.Context, token string) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM push_devices WHERE token=$1`, token); err != nil {
		return fmt.Errorf("delete push token: %w", err)
	}
	return nil
}
