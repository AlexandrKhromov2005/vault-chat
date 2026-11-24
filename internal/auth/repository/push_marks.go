package repository

import (
	"context"
	"fmt"
	"time"
)

// UnmarkedPushRecipients returns those of userIDs without a live mark for
// the signal, in their order.
func (r *SessionRepository) UnmarkedPushRecipients(ctx context.Context, signalID string, userIDs []string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT t.u::text FROM unnest($2::uuid[]) WITH ORDINALITY AS t(u, n)
WHERE NOT EXISTS (SELECT 1 FROM push_signal_marks m WHERE m.signal_id=$1 AND m.user_id=t.u AND m.expires_at > now())
ORDER BY t.n`, signalID, userIDs)
	if err != nil {
		return nil, fmt.Errorf("load push marks: %w", err)
	}
	defer rows.Close()
	var unmarked []string
	for rows.Next() {
		var user string
		if err := rows.Scan(&user); err != nil {
			return nil, fmt.Errorf("scan push mark: %w", err)
		}
		unmarked = append(unmarked, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load push marks: %w", err)
	}
	return unmarked, nil
}

// MarkPushRecipient records userID as handled for every signal in
// signalIDs until expires; an existing mark keeps the later expiry.
func (r *SessionRepository) MarkPushRecipient(ctx context.Context, signalIDs []string, userID string, expires time.Time) error {
	if _, err := r.pool.Exec(ctx, `INSERT INTO push_signal_marks (signal_id, user_id, expires_at)
SELECT DISTINCT unnest($1::text[]), $2::uuid, $3::timestamptz
ON CONFLICT (signal_id, user_id) DO UPDATE SET expires_at = GREATEST(push_signal_marks.expires_at, EXCLUDED.expires_at)`,
		signalIDs, userID, expires); err != nil {
		return fmt.Errorf("record push marks: %w", err)
	}
	return nil
}

// DeleteExpiredPushMarks deletes at most limit expired marks and returns how
// many it deleted.
func (r *SessionRepository) DeleteExpiredPushMarks(ctx context.Context, limit int) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM push_signal_marks WHERE ctid = ANY (ARRAY(
SELECT ctid FROM push_signal_marks WHERE expires_at <= now() LIMIT $1))`, limit)
	if err != nil {
		return 0, fmt.Errorf("delete expired push marks: %w", err)
	}
	return tag.RowsAffected(), nil
}
