package storage

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/media/domain"
)

func TestPreviewClaimsFencingAndVersionedPublication(t *testing.T) {
	pool := previewTestPool(t)
	repo := &Postgres{Pool: pool}
	ctx := context.Background()
	file := insertPreviewFile(t, pool, repo, "image/jpeg")
	require.NoError(t, repo.Ready(ctx, file.ID))

	claims := make(chan []domain.PreviewJob, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, owner := range []string{"one", "two"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			jobs, err := repo.ClaimPreviews(ctx, owner, 1, 30*time.Second)
			claims <- jobs
			errs <- err
		}()
	}
	wg.Wait()
	close(claims)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var claimed []domain.PreviewJob
	for jobs := range claims {
		claimed = append(claimed, jobs...)
	}
	require.Len(t, claimed, 1)
	job := claimed[0]
	require.Equal(t, 1, job.Attempts)

	key := "preview/" + file.ID + "/p320/" + uuid.NewString()
	require.NoError(t, repo.BeginPreview(ctx, job, key))
	stale := job
	stale.LeaseOwner = "expired-owner"
	require.ErrorIs(t, repo.CompletePreview(ctx, stale, key), domain.ErrStaleLease)
	_, err := repo.GetPreview(ctx, file.ID)
	require.ErrorIs(t, err, domain.ErrNotFound)
	require.NoError(t, repo.CompletePreview(ctx, job, key))
	gotKey, err := repo.GetPreview(ctx, file.ID)
	require.NoError(t, err)
	require.Equal(t, key, gotKey)

	v2 := domain.DecoderVersion + "-next"
	_, err = pool.Exec(ctx, "INSERT INTO preview_jobs(file_id,preset,decoder_version) VALUES($1::uuid,'p320',$2)", file.ID, v2)
	require.NoError(t, err)
	jobs, err := repo.ClaimPreviews(ctx, "next", 1, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.NoError(t, repo.FinishPreview(ctx, jobs[0], "failed", "decoder failed"))
	gotKey, err = repo.GetPreview(ctx, file.ID)
	require.NoError(t, err)
	require.Equal(t, key, gotKey)

	_, err = pool.Exec(ctx, "INSERT INTO preview_jobs(file_id,preset,decoder_version) VALUES($1::uuid,'p320','zig-0')", file.ID)
	require.NoError(t, err)
	jobs, err = repo.ClaimPreviews(ctx, "old", 1, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	oldKey := "preview/" + file.ID + "/p320/" + uuid.NewString()
	require.NoError(t, repo.BeginPreview(ctx, jobs[0], oldKey))
	require.NoError(t, repo.CompletePreview(ctx, jobs[0], oldKey))
	gotKey, err = repo.GetPreview(ctx, file.ID)
	require.NoError(t, err)
	require.Equal(t, key, gotKey)
	var oldState string
	require.NoError(t, pool.QueryRow(ctx, "SELECT state FROM preview_objects WHERE key=$1", oldKey).Scan(&oldState))
	require.Equal(t, "replaced", oldState)
}

func TestPreviewRetriesAndSweeper(t *testing.T) {
	pool := previewTestPool(t)
	repo := &Postgres{Pool: pool}
	ctx := context.Background()
	file := insertPreviewFile(t, pool, repo, "image/png")
	require.NoError(t, repo.Ready(ctx, file.ID))
	job := mustClaimPreview(t, repo, "one")
	require.NoError(t, repo.ReleasePreview(ctx, job, "S3 unavailable"))
	var attempts, infrastructure int
	var state string
	require.NoError(t, pool.QueryRow(ctx, "SELECT attempts,infrastructure_retries,state FROM preview_jobs WHERE file_id=$1::uuid", file.ID).Scan(&attempts, &infrastructure, &state))
	require.Equal(t, 0, attempts)
	require.Equal(t, 1, infrastructure)
	require.Equal(t, "pending", state)
	_, err := pool.Exec(ctx, "UPDATE preview_jobs SET next_attempt_at=now()-interval '1 second' WHERE file_id=$1::uuid", file.ID)
	require.NoError(t, err)

	job = mustClaimPreview(t, repo, "two")
	require.NoError(t, repo.RetryPreview(ctx, job, "decoder crashed"))
	require.NoError(t, pool.QueryRow(ctx, "SELECT attempts,state FROM preview_jobs WHERE file_id=$1::uuid", file.ID).Scan(&attempts, &state))
	require.Equal(t, 1, attempts)
	require.Equal(t, "pending", state)
	for attempt := 2; attempt <= 3; attempt++ {
		_, err = pool.Exec(ctx, "UPDATE preview_jobs SET next_attempt_at=now()-interval '1 second' WHERE file_id=$1::uuid", file.ID)
		require.NoError(t, err)
		job = mustClaimPreview(t, repo, "retry")
		require.Equal(t, attempt, job.Attempts)
		require.NoError(t, repo.RetryPreview(ctx, job, "decoder crashed"))
	}
	require.NoError(t, pool.QueryRow(ctx, "SELECT state FROM preview_jobs WHERE file_id=$1::uuid", file.ID).Scan(&state))
	require.Equal(t, "failed", state)

	other := insertPreviewFile(t, pool, repo, "image/png")
	require.NoError(t, repo.Ready(ctx, other.ID))
	inserted, err := repo.SweepPreviews(ctx, domain.DecoderVersion, 100)
	require.NoError(t, err)
	require.Equal(t, 0, inserted)
	_, err = pool.Exec(ctx, "DELETE FROM preview_jobs WHERE file_id=$1::uuid", other.ID)
	require.NoError(t, err)
	inserted, err = repo.SweepPreviews(ctx, domain.DecoderVersion, 100)
	require.NoError(t, err)
	require.Equal(t, 1, inserted)
	inserted, err = repo.SweepPreviews(ctx, domain.DecoderVersion, 100)
	require.NoError(t, err)
	require.Zero(t, inserted)
}

func TestPreviewObjectGarbageCollectionProtectsCurrentPointer(t *testing.T) {
	pool := previewTestPool(t)
	repo := &Postgres{Pool: pool}
	ctx := context.Background()
	file := insertPreviewFile(t, pool, repo, "image/png")
	require.NoError(t, repo.Ready(ctx, file.ID))
	job := mustClaimPreview(t, repo, "one")
	key := "preview/" + file.ID + "/p320/" + uuid.NewString()
	require.NoError(t, repo.BeginPreview(ctx, job, key))
	require.NoError(t, repo.CompletePreview(ctx, job, key))
	_, err := pool.Exec(ctx, "UPDATE preview_objects SET created_at=now()-interval '2 hours' WHERE key=$1", key)
	require.NoError(t, err)
	objects, err := repo.ExpiredPreviewObjects(ctx, 100)
	require.NoError(t, err)
	require.Empty(t, objects)
	require.Error(t, repo.DeletePreviewObject(ctx, key))
}

func mustClaimPreview(t *testing.T, repo *Postgres, owner string) domain.PreviewJob {
	t.Helper()
	jobs, err := repo.ClaimPreviews(context.Background(), owner, 1, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	return jobs[0]
}
