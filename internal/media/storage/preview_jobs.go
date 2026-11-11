package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/AlexandrKhromov2005/vault-chat/internal/media/domain"
)

func (p *Postgres) ClaimPreviews(ctx context.Context, owner string, limit int, lease time.Duration) ([]domain.PreviewJob, error) {
	rows, err := p.Pool.Query(ctx, `WITH candidates AS (
		SELECT file_id,preset,decoder_version FROM preview_jobs
		WHERE (state='pending' AND next_attempt_at<=now()) OR (state='running' AND lease_until<now())
		ORDER BY next_attempt_at LIMIT $2 FOR UPDATE SKIP LOCKED
	) UPDATE preview_jobs AS j SET state='running',lease_owner=$1,
		lease_until=now()+$3::interval,attempts=j.attempts+1
		FROM candidates AS c
		WHERE j.file_id=c.file_id AND j.preset=c.preset AND j.decoder_version=c.decoder_version
		RETURNING j.file_id::text,j.preset,j.decoder_version,j.state,j.attempts,
		j.infrastructure_retries,j.next_attempt_at,j.lease_owner,j.lease_until`, owner, limit, lease.String())
	if err != nil {
		return nil, fmt.Errorf("claim previews: %w", err)
	}
	jobs := make([]domain.PreviewJob, 0, limit)
	for rows.Next() {
		var job domain.PreviewJob
		if err := rows.Scan(&job.FileID, &job.Preset, &job.DecoderVersion, &job.State, &job.Attempts, &job.InfrastructureRetries, &job.NextAttemptAt, &job.LeaseOwner, &job.LeaseUntil); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read claimed preview: %w", err)
		}
		jobs = append(jobs, job)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read preview claims: %w", err)
	}
	return jobs, nil
}

func (p *Postgres) BeginPreview(ctx context.Context, job domain.PreviewJob, key string) error {
	tag, err := p.Pool.Exec(ctx, `INSERT INTO preview_objects(key,file_id)
		SELECT $2,$1::uuid FROM preview_jobs
		WHERE file_id=$1::uuid AND preset=$3 AND decoder_version=$4 AND state='running'
		AND lease_owner=$5 AND attempts=$6 AND lease_until>=now()`,
		job.FileID, key, job.Preset, job.DecoderVersion, job.LeaseOwner, job.Attempts)
	if err != nil {
		return fmt.Errorf("begin preview object: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrStaleLease
	}
	return nil
}

func (p *Postgres) ReleasePreview(ctx context.Context, job domain.PreviewJob, reason string) error {
	_, err := p.Pool.Exec(ctx, `UPDATE preview_jobs SET state='pending',attempts=attempts-1,
		infrastructure_retries=least(infrastructure_retries+1,6),
		next_attempt_at=now()+least(interval '10 minutes',interval '30 seconds'*power(2,least(infrastructure_retries,5))),
		lease_owner=NULL,lease_until=NULL,last_error=$4
		WHERE file_id=$1::uuid AND preset=$2 AND decoder_version=$3 AND state='running'
		AND lease_owner=$5 AND attempts=$6`, job.FileID, job.Preset, job.DecoderVersion, reason, job.LeaseOwner, job.Attempts)
	if err != nil {
		return fmt.Errorf("release preview after infrastructure failure: %w", err)
	}
	return nil
}

func (p *Postgres) RetryPreview(ctx context.Context, job domain.PreviewJob, reason string) error {
	_, err := p.Pool.Exec(ctx, `UPDATE preview_jobs SET
		state=CASE WHEN attempts>=3 THEN 'failed' ELSE 'pending' END,
		next_attempt_at=now()+CASE attempts WHEN 1 THEN interval '30 seconds' ELSE interval '5 minutes' END,
		lease_owner=NULL,lease_until=NULL,last_error=$4
		WHERE file_id=$1::uuid AND preset=$2 AND decoder_version=$3 AND state='running'
		AND lease_owner=$5 AND attempts=$6`, job.FileID, job.Preset, job.DecoderVersion, reason, job.LeaseOwner, job.Attempts)
	if err != nil {
		return fmt.Errorf("retry failed preview: %w", err)
	}
	return nil
}

func (p *Postgres) FinishPreview(ctx context.Context, job domain.PreviewJob, state, reason string) error {
	if state != "failed" && state != "skipped" {
		return fmt.Errorf("invalid terminal preview state %q", state)
	}
	_, err := p.Pool.Exec(ctx, `UPDATE preview_jobs SET state=$4,lease_owner=NULL,lease_until=NULL,last_error=$5
		WHERE file_id=$1::uuid AND preset=$2 AND decoder_version=$3 AND state='running'
		AND lease_owner=$6 AND attempts=$7`, job.FileID, job.Preset, job.DecoderVersion, state, reason, job.LeaseOwner, job.Attempts)
	if err != nil {
		return fmt.Errorf("finish preview: %w", err)
	}
	return nil
}

func (p *Postgres) CompletePreview(ctx context.Context, job domain.PreviewJob, key string) error {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin preview publication: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE preview_jobs SET state='ready',lease_owner=NULL,lease_until=NULL,last_error=NULL
		WHERE file_id=$1::uuid AND preset=$2 AND decoder_version=$3 AND state='running'
		AND lease_owner=$4 AND attempts=$5 AND lease_until>=now()`, job.FileID, job.Preset, job.DecoderVersion, job.LeaseOwner, job.Attempts)
	if err != nil {
		return fmt.Errorf("complete preview job: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrStaleLease
	}
	tag, err = tx.Exec(ctx, `UPDATE preview_objects SET state='ready' WHERE key=$1 AND file_id=$2::uuid AND state='uploading'`, key, job.FileID)
	if err != nil {
		return fmt.Errorf("mark preview object ready: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrStaleLease
	}
	if _, err = tx.Exec(ctx, `INSERT INTO file_previews(file_id,preset,key,decoder_version) VALUES($1::uuid,$2,$3,$4)
		ON CONFLICT(file_id,preset) DO UPDATE SET key=EXCLUDED.key,decoder_version=EXCLUDED.decoder_version,updated_at=now()
		WHERE file_previews.decoder_version<=EXCLUDED.decoder_version`, job.FileID, job.Preset, key, job.DecoderVersion); err != nil {
		return fmt.Errorf("switch visible preview: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE preview_objects AS o SET state='replaced',replaced_at=now()
		WHERE o.key=$1 AND NOT EXISTS(SELECT 1 FROM file_previews AS f WHERE f.key=o.key)`, key); err != nil {
		return fmt.Errorf("retire superseded preview render: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE preview_objects AS o SET state='replaced',replaced_at=now()
		WHERE o.file_id=$1::uuid AND o.key<>$2 AND o.state='ready'
		AND EXISTS(SELECT 1 FROM file_previews AS f WHERE f.file_id=o.file_id AND f.preset=$3)`, job.FileID, key, job.Preset); err != nil {
		return fmt.Errorf("retire replaced previews: %w", err)
	}
	return tx.Commit(ctx)
}

func (p *Postgres) GetPreview(ctx context.Context, fileID string) (string, error) {
	var key string
	err := p.Pool.QueryRow(ctx, `SELECT key FROM file_previews WHERE file_id=$1::uuid AND preset='p320'`, fileID).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read visible preview: %w", err)
	}
	return key, nil
}

func (p *Postgres) SweepPreviews(ctx context.Context, version string, limit int) (int, error) {
	tag, err := p.Pool.Exec(ctx, `INSERT INTO preview_jobs(file_id,preset,decoder_version)
		SELECT f.id,'p320',$1 FROM files AS f
		WHERE f.ready AND f.content_type IN ('image/png','image/jpeg')
		AND NOT EXISTS(SELECT 1 FROM preview_jobs AS j WHERE j.file_id=f.id AND j.preset='p320' AND j.decoder_version=$1)
		ORDER BY f.created_at LIMIT $2 ON CONFLICT DO NOTHING`, version, limit)
	if err != nil {
		return 0, fmt.Errorf("sweep missing previews: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (p *Postgres) ExpiredPreviewObjects(ctx context.Context, limit int) ([]domain.PreviewObject, error) {
	rows, err := p.Pool.Query(ctx, `SELECT o.key,o.file_id::text,o.state,o.created_at FROM preview_objects AS o
		WHERE ((o.state='uploading' AND o.created_at<now()-interval '1 hour')
		OR (o.state='replaced' AND o.replaced_at<now()-interval '1 hour')
		OR (o.state='collecting' AND o.created_at<now()-interval '5 minutes'))
		AND NOT EXISTS(SELECT 1 FROM file_previews AS f WHERE f.key=o.key)
		ORDER BY o.created_at LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list expired preview objects: %w", err)
	}
	objects := make([]domain.PreviewObject, 0)
	for rows.Next() {
		var object domain.PreviewObject
		if err := rows.Scan(&object.Key, &object.FileID, &object.State, &object.CreatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read expired preview object: %w", err)
		}
		objects = append(objects, object)
	}
	rows.Close()
	return objects, rows.Err()
}

func (p *Postgres) ClaimPreviewObject(ctx context.Context, key string) error {
	tag, err := p.Pool.Exec(ctx, `UPDATE preview_objects AS o SET state='collecting'
		WHERE o.key=$1 AND ((o.state='uploading' AND o.created_at<now()-interval '1 hour')
		OR (o.state='replaced' AND o.replaced_at<now()-interval '1 hour')
		OR (o.state='collecting' AND o.created_at<now()-interval '5 minutes'))
		AND NOT EXISTS(SELECT 1 FROM file_previews AS f WHERE f.key=o.key)`, key)
	if err != nil {
		return fmt.Errorf("claim preview object for collection: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrNotFound
	}
	return nil
}

func (p *Postgres) ReleasePreviewObject(ctx context.Context, key string) error {
	_, err := p.Pool.Exec(ctx, `UPDATE preview_objects SET state=CASE WHEN replaced_at IS NULL THEN 'uploading' ELSE 'replaced' END
		WHERE key=$1 AND state='collecting'`, key)
	if err != nil {
		return fmt.Errorf("release preview collection: %w", err)
	}
	return nil
}

func (p *Postgres) DeletePreviewObject(ctx context.Context, key string) error {
	tag, err := p.Pool.Exec(ctx, `DELETE FROM preview_objects AS o WHERE o.key=$1 AND o.state='collecting'
		AND NOT EXISTS(SELECT 1 FROM file_previews AS f WHERE f.key=o.key)`, key)
	if err != nil {
		return fmt.Errorf("delete collected preview metadata: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrNotFound
	}
	return nil
}

// PreviewQueueStats reports queued work and the age of its oldest job.
func (p *Postgres) PreviewQueueStats(ctx context.Context) (int, float64, error) {
	var count int
	var age float64
	err := p.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state IN ('pending','running')),
		coalesce(extract(epoch FROM now()-min(created_at) FILTER (WHERE state IN ('pending','running'))),0)
		FROM preview_jobs`).Scan(&count, &age)
	if err != nil {
		return 0, 0, fmt.Errorf("read preview queue metrics: %w", err)
	}
	return count, age, nil
}
