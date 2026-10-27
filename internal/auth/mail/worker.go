package mail

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

var errQueue = errors.New("mail queue unavailable")

// Job is a leased encrypted notification. Attempts includes the current claim.
type Job struct {
	ID, Lease string
	Payload   []byte
	Attempts  int
	ExpiresAt time.Time
}

// Queue must atomically lease jobs and fence state changes by ID and lease.
// A zero retry time permanently discards the job.
type Queue interface {
	Claim(context.Context) (*Job, error)
	Complete(context.Context, string, string) error
	Retry(context.Context, string, string, time.Time) error
}

// Sender delivers one notification, obeying its context deadline.
type Sender interface {
	Send(context.Context, Message) error
}

// Worker serializes sends, discards unusable jobs and schedules bounded retries.
type Worker struct {
	queue  Queue
	sender Sender
	box    *Box
	logger *slog.Logger
	gate   chan struct{}
}

// NewWorker creates a worker with at most one in-flight send, including when
// multiple callers invoke Step concurrently.
func NewWorker(queue Queue, sender Sender, box *Box, logger *slog.Logger) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{queue: queue, sender: sender, box: box, logger: logger, gate: make(chan struct{}, 1)}
}

// Step claims at most one job. A handled delivery failure returns true,nil after
// saving a retry/drop; queue failures are opaque and leave recovery to the lease.
func (w *Worker) Step(ctx context.Context) (bool, error) {
	if w == nil || w.queue == nil || w.sender == nil || w.box == nil {
		return false, errQueue
	}
	select {
	case w.gate <- struct{}{}:
		defer func() { <-w.gate }()
	case <-ctx.Done():
		return false, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	job, err := w.queue.Claim(claimCtx)
	cancel()
	if err != nil {
		return false, errQueue
	}
	if job == nil {
		return false, nil
	}
	now := time.Now()
	message, openErr := w.box.Open(job.Payload)
	if !job.ExpiresAt.After(now) || job.Attempts < 0 || job.Attempts > 5 || openErr != nil {
		return true, w.retry(ctx, job, time.Time{}, "discarded")
	}
	deadline := now.Add(10 * time.Second)
	if job.ExpiresAt.Before(deadline) {
		deadline = job.ExpiresAt
	}
	sendCtx, cancel := context.WithDeadline(ctx, deadline)
	err = w.sender.Send(sendCtx, message)
	cancel()
	if err == nil {
		ackCtx, ackCancel := context.WithTimeout(ctx, 5*time.Second)
		defer ackCancel()
		if err := w.queue.Complete(ackCtx, job.ID, job.Lease); err != nil {
			w.logger.Warn("mail job persistence failed", slog.String("job_id", job.ID), slog.Int("attempts", job.Attempts))
			return true, errQueue
		}
		return true, nil
	}
	attempt := max(job.Attempts, 1)
	next := time.Time{}
	if attempt < 5 {
		next = time.Now().Add(time.Minute * time.Duration(1<<uint(attempt-1)))
		if !next.Before(job.ExpiresAt) {
			next = time.Time{}
		}
	}
	return true, w.retry(ctx, job, next, "delivery_failed")
}

func (w *Worker) retry(ctx context.Context, job *Job, next time.Time, outcome string) error {
	w.logger.Warn("mail job handled", slog.String("job_id", job.ID), slog.Int("attempts", job.Attempts), slog.String("outcome", outcome))
	retryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := w.queue.Retry(retryCtx, job.ID, job.Lease, next); err != nil {
		return errQueue
	}
	return nil
}

// Run checks the queue once per second and returns after context cancellation.
// It never logs errors supplied by the queue, SMTP server or message payload.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if _, err := w.Step(ctx); err != nil && ctx.Err() == nil {
			w.logger.Warn("mail worker unavailable")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
