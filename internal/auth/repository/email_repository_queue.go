package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/mail"
)

// Claim reserves a job before SMTP starts; crashes therefore count as attempts.
// An expired lease can be reclaimed, but the fifth attempt can never be sent twice.
func (r *EmailRepository) Claim(ctx context.Context) (*mail.Job, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollbackEmailTransaction(ctx, tx)
	// Audit cascades and producers touch the same action/outbox rows. Keep
	// maintenance under their global lock, released before any SMTP work.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('auth.email.outbox',0))`); err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `WITH expired AS (SELECT id FROM email_outbox
WHERE expires_at<=clock_timestamp() OR (attempts>=5 AND (lease_until IS NULL OR lease_until<=clock_timestamp()))
ORDER BY expires_at LIMIT 1000 FOR UPDATE SKIP LOCKED)
DELETE FROM email_outbox o USING expired e WHERE o.id=e.id`)
	if err != nil {
		return nil, err
	}
	// Keep request audit longer than the hourly quota, even after delivery/use.
	_, err = tx.Exec(ctx, `WITH expired AS (SELECT id FROM email_actions
WHERE created_at<clock_timestamp()-interval '24 hours' AND expires_at<=clock_timestamp()
ORDER BY expires_at LIMIT 1000 FOR UPDATE SKIP LOCKED)
DELETE FROM email_actions a USING expired e WHERE a.id=e.id`)
	if err != nil {
		return nil, err
	}
	job := &mail.Job{}
	err = tx.QueryRow(ctx, `WITH ready AS (SELECT id FROM email_outbox
WHERE expires_at>clock_timestamp() AND available_at<=clock_timestamp() AND attempts<5
AND (lease_until IS NULL OR lease_until<=clock_timestamp())
ORDER BY created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED)
UPDATE email_outbox o SET attempts=o.attempts+1,lease=$1::uuid,lease_until=clock_timestamp()+interval '60 seconds'
FROM ready WHERE o.id=ready.id RETURNING o.id::text,o.lease::text,o.payload,o.attempts,o.expires_at`, uuid.NewString()).
		Scan(&job.ID, &job.Lease, &job.Payload, &job.Attempts, &job.ExpiresAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	if job.ID == "" {
		return nil, nil
	}
	return job, nil
}

// Complete is idempotent; a late acknowledgement cannot delete a new lease.
func (r *EmailRepository) Complete(ctx context.Context, id, lease string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := r.pool.Exec(ctx, `DELETE FROM email_outbox WHERE id=$1::uuid AND lease=$2::uuid`, id, lease)
	return err
}

// Retry drops expired/exhausted work, or releases the current lease until next.
func (r *EmailRepository) Retry(ctx context.Context, id, lease string, next time.Time) error {
	if next.IsZero() {
		return r.Complete(ctx, id, lease)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackEmailTransaction(ctx, tx)
	_, err = tx.Exec(ctx, `DELETE FROM email_outbox WHERE id=$1::uuid AND lease=$2::uuid
AND (expires_at<=clock_timestamp() OR attempts>=5)`, id, lease)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE email_outbox SET available_at=GREATEST($3,clock_timestamp()+interval '1 second'),lease=NULL,lease_until=NULL
WHERE id=$1::uuid AND lease=$2::uuid`, id, lease, next)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
