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

// DeletePushToken removes a token the provider no longer accepts, whichever
// login holds it now.
func (r *SessionRepository) DeletePushToken(ctx context.Context, token string) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM push_devices WHERE token=$1`, token); err != nil {
		return fmt.Errorf("delete push token: %w", err)
	}
	return nil
}
