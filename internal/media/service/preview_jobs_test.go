package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/media/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/media/preview"
)

type previewRepoFake struct {
	mu           sync.Mutex
	jobs         []domain.PreviewJob
	claimN       int
	beginKey     []string
	released     *domain.PreviewJob
	retried      *domain.PreviewJob
	finished     *domain.PreviewJob
	finishState  string
	completedKey []string
	completeErr  error
	swept        int
	objects      []domain.PreviewObject
	deleted      []string
}

func (r *previewRepoFake) ClaimPreviews(_ context.Context, _ string, limit int, _ time.Duration) ([]domain.PreviewJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.claimN = limit
	return append([]domain.PreviewJob(nil), r.jobs...), nil
}
func (r *previewRepoFake) BeginPreview(_ context.Context, _ domain.PreviewJob, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.beginKey = append(r.beginKey, key)
	return nil
}
func (r *previewRepoFake) ReleasePreview(_ context.Context, job domain.PreviewJob, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.released = &job
	return nil
}
func (r *previewRepoFake) RetryPreview(_ context.Context, job domain.PreviewJob, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retried = &job
	return nil
}
func (r *previewRepoFake) FinishPreview(_ context.Context, job domain.PreviewJob, state, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finished = &job
	r.finishState = state
	return nil
}
func (r *previewRepoFake) CompletePreview(_ context.Context, _ domain.PreviewJob, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completedKey = append(r.completedKey, key)
	return r.completeErr
}
func (r *previewRepoFake) SweepPreviews(context.Context, string, int) (int, error) {
	r.swept++
	return 1, nil
}
func (r *previewRepoFake) ExpiredPreviewObjects(context.Context, int) ([]domain.PreviewObject, error) {
	return r.objects, nil
}
func (r *previewRepoFake) ClaimPreviewObject(context.Context, string) error   { return nil }
func (r *previewRepoFake) ReleasePreviewObject(context.Context, string) error { return nil }
func (r *previewRepoFake) DeletePreviewObject(_ context.Context, key string) error {
	r.deleted = append(r.deleted, key)
	return nil
}

type previewStoreFake struct {
	mu        sync.Mutex
	data      []byte
	openErr   error
	putErr    error
	removeErr error
	puts      map[string][]byte
	removed   []string
}

func (s *previewStoreFake) Put(_ context.Context, key string, data []byte, typ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.putErr != nil {
		return s.putErr
	}
	if typ != "image/jpeg" {
		return errors.New("wrong content type")
	}
	if s.puts == nil {
		s.puts = map[string][]byte{}
	}
	s.puts[key] = append([]byte(nil), data...)
	return nil
}
func (s *previewStoreFake) Open(context.Context, string) (io.ReadCloser, error) {
	if s.openErr != nil {
		return nil, s.openErr
	}
	return io.NopCloser(bytes.NewReader(s.data)), nil
}
func (s *previewStoreFake) Remove(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.removeErr != nil {
		return s.removeErr
	}
	s.removed = append(s.removed, key)
	return nil
}

type previewDecoderFake struct {
	mu        sync.Mutex
	calls     int
	active    int
	maxActive int
	result    preview.Result
	err       error
	started   chan struct{}
	release   chan struct{}
}

func (d *previewDecoderFake) Decode(ctx context.Context, _ []byte) (preview.Result, error) {
	d.mu.Lock()
	d.calls++
	d.active++
	if d.active > d.maxActive {
		d.maxActive = d.active
	}
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.active--
		d.mu.Unlock()
	}()
	if d.started != nil {
		d.started <- struct{}{}
	}
	if d.release != nil {
		select {
		case <-d.release:
		case <-ctx.Done():
			return preview.Result{}, ctx.Err()
		}
	}
	return d.result, d.err
}

func previewFixture(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	require.NoError(t, png.Encode(&out, image.NewNRGBA(image.Rect(0, 0, 2, 1))))
	return out.Bytes()
}
func previewJob() domain.PreviewJob {
	return domain.PreviewJob{FileID: uuid.NewString(), Preset: domain.PreviewPreset, DecoderVersion: domain.DecoderVersion, State: "running", Attempts: 1, LeaseOwner: "scheduler-test"}
}
func previewScheduler(repo *previewRepoFake, store *previewStoreFake, decoders ...preview.Decoder) *PreviewScheduler {
	return NewPreviewScheduler(repo, store, decoders, "scheduler-test")
}
func validResult() preview.Result {
	return preview.Result{Width: 2, Height: 1, RGB: make([]byte, 6)}
}

func TestPreviewPublishesBoundedJPEGWithoutMetadata(t *testing.T) {
	repo := &previewRepoFake{jobs: []domain.PreviewJob{previewJob()}}
	store := &previewStoreFake{data: previewFixture(t)}
	decoder := &previewDecoderFake{result: validResult()}
	require.NoError(t, previewScheduler(repo, store, decoder).RunOnce(context.Background()))
	require.Len(t, store.puts, 1)
	require.Equal(t, repo.beginKey, repo.completedKey)
	require.True(t, strings.HasPrefix(repo.completedKey[0], "preview/"+repo.jobs[0].FileID+"/p320/"))
	for key, data := range store.puts {
		require.LessOrEqual(t, len(data), preview.MaxOutput)
		require.NotContains(t, data, []byte("Exif"))
		img, err := jpeg.Decode(bytes.NewReader(data))
		require.NoError(t, err, key)
		require.Equal(t, 2, img.Bounds().Dx())
	}
}

func TestPreviewInfrastructureFailuresDoNotConsumeAttempts(t *testing.T) {
	for _, test := range []struct {
		name       string
		store      *previewStoreFake
		decoderErr error
	}{
		{name: "original read", store: &previewStoreFake{openErr: errors.New("S3 down")}},
		{name: "preview write", store: &previewStoreFake{putErr: errors.New("S3 down")}},
		{name: "workers unavailable", decoderErr: preview.ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &previewRepoFake{jobs: []domain.PreviewJob{previewJob()}}
			store := test.store
			if store == nil {
				store = &previewStoreFake{}
			}
			if store.data == nil {
				store.data = previewFixture(t)
			}
			decoder := &previewDecoderFake{result: validResult(), err: test.decoderErr}
			require.NoError(t, previewScheduler(repo, store, decoder).RunOnce(context.Background()))
			require.NotNil(t, repo.released)
			require.Nil(t, repo.retried)
			require.Empty(t, repo.completedKey)
		})
	}
}

func TestPreviewDecoderFailuresAreBoundedAndClassified(t *testing.T) {
	for _, test := range []struct {
		name      string
		err       error
		result    preview.Result
		attempts  int
		wantState string
		wantRetry bool
	}{
		{name: "worker retry", err: preview.ErrWorker, attempts: 1, wantRetry: true},
		{name: "worker exhausted", err: preview.ErrWorker, attempts: 3, wantRetry: true},
		{name: "corrupt", err: preview.ErrInvalid, attempts: 1, wantState: "failed"},
		{name: "budget", err: preview.ErrSkipped, attempts: 1, wantState: "skipped"},
		{name: "bad dimensions", result: preview.Result{Width: 3, Height: 1, RGB: make([]byte, 9)}, attempts: 1, wantRetry: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			job := previewJob()
			job.Attempts = test.attempts
			repo := &previewRepoFake{jobs: []domain.PreviewJob{job}}
			store := &previewStoreFake{data: previewFixture(t)}
			result := test.result
			if result.Width == 0 {
				result = validResult()
			}
			require.NoError(t, previewScheduler(repo, store, &previewDecoderFake{result: result, err: test.err}).RunOnce(context.Background()))
			require.Equal(t, test.wantRetry, repo.retried != nil)
			require.Equal(t, test.wantState, repo.finishState)
			require.Nil(t, repo.released)
		})
	}
}

func TestPreviewStaleCompletionDoesNotRetryOrPublish(t *testing.T) {
	repo := &previewRepoFake{jobs: []domain.PreviewJob{previewJob()}, completeErr: domain.ErrStaleLease}
	store := &previewStoreFake{data: previewFixture(t)}
	require.NoError(t, previewScheduler(repo, store, &previewDecoderFake{result: validResult()}).RunOnce(context.Background()))
	require.Nil(t, repo.released)
	require.Nil(t, repo.retried)
}

func TestPreviewSchedulerRunsAtMostTwoJobs(t *testing.T) {
	repo := &previewRepoFake{jobs: []domain.PreviewJob{previewJob(), previewJob()}}
	store := &previewStoreFake{data: previewFixture(t)}
	decoder := &previewDecoderFake{result: validResult(), started: make(chan struct{}, 2), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- previewScheduler(repo, store, decoder).RunOnce(context.Background()) }()
	for range 2 {
		<-decoder.started
	}
	close(decoder.release)
	require.NoError(t, <-done)
	require.Equal(t, 2, repo.claimN)
	require.Equal(t, 2, decoder.maxActive)
}

func TestPreviewMaintenanceSweepsAndCollectsOnlyAfterS3Removal(t *testing.T) {
	repo := &previewRepoFake{objects: []domain.PreviewObject{{Key: "old"}, {Key: "failed"}}}
	store := &previewStoreFake{removeErr: errors.New("S3 down")}
	scheduler := previewScheduler(repo, store)
	require.Error(t, scheduler.Maintain(context.Background()))
	require.Equal(t, 1, repo.swept)
	require.Empty(t, repo.deleted)
	store.removeErr = nil
	require.NoError(t, scheduler.Maintain(context.Background()))
	require.Equal(t, []string{"old", "failed"}, repo.deleted)
}

func TestPreviewPublicationOutageKeepsAttemptRetryable(t *testing.T) {
	repo := &previewRepoFake{jobs: []domain.PreviewJob{previewJob()}, completeErr: errors.New("database down")}
	store := &previewStoreFake{data: previewFixture(t)}
	require.NoError(t, previewScheduler(repo, store, &previewDecoderFake{result: validResult()}).RunOnce(context.Background()))
	require.NotNil(t, repo.released)
	require.Nil(t, repo.retried)
	require.Len(t, store.puts, 1)
}
