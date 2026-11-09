package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/AlexandrKhromov2005/vault-chat/internal/media/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/media/preview"
)

const (
	previewSlots       = 2
	previewLease       = 30 * time.Second
	previewTaskTimeout = 10 * time.Second
	previewSweepLimit  = 100
)

// PreviewRepository persists leases, immutable objects and the visible pointer.
type PreviewRepository interface {
	ClaimPreviews(context.Context, string, int, time.Duration) ([]domain.PreviewJob, error)
	BeginPreview(context.Context, domain.PreviewJob, string) error
	ReleasePreview(context.Context, domain.PreviewJob, string) error
	RetryPreview(context.Context, domain.PreviewJob, string) error
	FinishPreview(context.Context, domain.PreviewJob, string, string) error
	CompletePreview(context.Context, domain.PreviewJob, string) error
	SweepPreviews(context.Context, string, int) (int, error)
	ExpiredPreviewObjects(context.Context, int) ([]domain.PreviewObject, error)
	ClaimPreviewObject(context.Context, string) error
	ReleasePreviewObject(context.Context, string) error
	DeletePreviewObject(context.Context, string) error
}

// PreviewScheduler runs at most two isolated decoder calls at a time.
type PreviewScheduler struct {
	repo     PreviewRepository
	blobs    Store
	workers  []preview.Decoder
	owner    string
	renderID func() string
}

func NewPreviewScheduler(repo PreviewRepository, blobs Store, workers []preview.Decoder, owner string) *PreviewScheduler {
	return &PreviewScheduler{repo: repo, blobs: blobs, workers: workers, owner: owner, renderID: uuid.NewString}
}

func (s *PreviewScheduler) RunOnce(ctx context.Context) error {
	jobs, err := s.repo.ClaimPreviews(ctx, s.owner, previewSlots, previewLease)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(jobs))
	for _, job := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.runJob(ctx, job); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	var result error
	for err := range errs {
		result = errors.Join(result, err)
	}
	return result
}

func (s *PreviewScheduler) runJob(ctx context.Context, job domain.PreviewJob) error {
	ctx, cancel := context.WithTimeout(ctx, previewTaskTimeout)
	defer cancel()
	key := "preview/" + job.FileID + "/" + job.Preset + "/" + s.renderID()
	if err := s.repo.BeginPreview(ctx, job, key); err != nil {
		if errors.Is(err, domain.ErrStaleLease) {
			return nil
		}
		return s.release(ctx, job, "metadata unavailable", err)
	}
	body, err := s.blobs.Open(ctx, job.FileID)
	if err != nil {
		return s.release(ctx, job, "original storage unavailable", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(body, preview.MaxInput+1))
	closeErr := body.Close()
	if readErr != nil || closeErr != nil || len(data) > preview.MaxInput {
		return s.release(ctx, job, "original storage unavailable", errors.Join(readErr, closeErr))
	}
	result, err := s.decode(ctx, data)
	if errors.Is(err, preview.ErrUnavailable) {
		return s.release(ctx, job, "preview workers unavailable", err)
	}
	if errors.Is(err, preview.ErrInvalid) {
		return s.repo.FinishPreview(ctx, job, "failed", "invalid image")
	}
	if errors.Is(err, preview.ErrSkipped) {
		return s.repo.FinishPreview(ctx, job, "skipped", "image exceeds preview budget")
	}
	if err != nil {
		return s.repo.RetryPreview(ctx, job, "decoder failed")
	}
	if err := preview.ValidateResult(data, result); err != nil {
		if errors.Is(err, preview.ErrInvalid) {
			return s.repo.FinishPreview(ctx, job, "failed", "invalid image")
		}
		if errors.Is(err, preview.ErrSkipped) {
			return s.repo.FinishPreview(ctx, job, "skipped", "image exceeds preview budget")
		}
		return s.repo.RetryPreview(ctx, job, "invalid decoder response")
	}
	encoded, err := preview.EncodeJPEG(result)
	if err != nil {
		return s.repo.RetryPreview(ctx, job, "preview encoding failed")
	}
	if err := s.blobs.Put(ctx, key, encoded, "image/jpeg"); err != nil {
		return s.release(ctx, job, "preview storage unavailable", err)
	}
	if err := s.repo.CompletePreview(ctx, job, key); err != nil {
		if errors.Is(err, domain.ErrStaleLease) {
			return nil
		}
		return s.release(ctx, job, "preview publication unavailable", err)
	}
	return nil
}

func (s *PreviewScheduler) decode(ctx context.Context, data []byte) (preview.Result, error) {
	var unavailable error
	for _, worker := range s.workers {
		result, err := worker.Decode(ctx, data)
		if err == nil {
			return result, nil
		}
		if errors.Is(err, preview.ErrUnavailable) {
			unavailable = errors.Join(unavailable, err)
			continue
		}
		return preview.Result{}, err
	}
	if unavailable != nil {
		return preview.Result{}, unavailable
	}
	return preview.Result{}, preview.ErrUnavailable
}

func (s *PreviewScheduler) release(ctx context.Context, job domain.PreviewJob, reason string, cause error) error {
	if err := s.repo.ReleasePreview(ctx, job, reason); err != nil {
		return errors.Join(cause, err)
	}
	return nil
}

func (s *PreviewScheduler) Maintain(ctx context.Context) error {
	_, err := s.repo.SweepPreviews(ctx, domain.DecoderVersion, previewSweepLimit)
	if err != nil {
		return err
	}
	objects, err := s.repo.ExpiredPreviewObjects(ctx, previewSweepLimit)
	if err != nil {
		return err
	}
	var result error
	for _, object := range objects {
		if err := s.repo.ClaimPreviewObject(ctx, object.Key); err != nil {
			if !errors.Is(err, domain.ErrNotFound) {
				result = errors.Join(result, err)
			}
			continue
		}
		if err := s.blobs.Remove(ctx, object.Key); err != nil {
			result = errors.Join(result, err, s.repo.ReleasePreviewObject(ctx, object.Key))
			continue
		}
		if err := s.repo.DeletePreviewObject(ctx, object.Key); err != nil {
			result = errors.Join(result, fmt.Errorf("delete preview metadata: %w", err))
		}
	}
	return result
}
