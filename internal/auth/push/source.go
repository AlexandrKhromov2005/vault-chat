package push

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/pushbus"
)

// DeclaringSource pulls from Auth's consumer, declaring the stream and the
// consumer before the first pull and again when the broker lost them, as
// after starting on an empty store, when the pull has no responder. It is
// used by one goroutine.
type DeclaringSource struct {
	js       jetstream.JetStream
	consumer jetstream.Consumer
	timeout  time.Duration
}

// NewDeclaringSource binds Auth's JetStream context.
func NewDeclaringSource(js jetstream.JetStream) *DeclaringSource {
	return &DeclaringSource{js: js, timeout: 5 * time.Second}
}

// Next declares if needed and pulls one signal.
func (s *DeclaringSource) Next(opts ...jetstream.FetchOpt) (jetstream.Msg, error) {
	if s.consumer == nil {
		ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
		defer cancel()
		consumer, err := pushbus.Declare(ctx, s.js)
		if err != nil {
			return nil, err
		}
		s.consumer = consumer
	}
	msg, err := s.consumer.Next(opts...)
	if errors.Is(err, jetstream.ErrConsumerNotFound) || errors.Is(err, jetstream.ErrConsumerDeleted) ||
		errors.Is(err, jetstream.ErrStreamNotFound) || errors.Is(err, nats.ErrNoResponders) {
		s.consumer = nil
	}
	return msg, err
}

// MarkCleaner deletes expired marks.
type MarkCleaner interface {
	DeleteExpiredPushMarks(ctx context.Context, limit int) (int64, error)
}

const markBatch = 10000

// CleanMarks deletes expired marks every interval until ctx ends, in
// batches so that one statement never holds many rows.
func CleanMarks(ctx context.Context, store MarkCleaner, interval time.Duration, logger *slog.Logger) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		for ctx.Err() == nil {
			deleted, err := store.DeleteExpiredPushMarks(ctx, markBatch)
			if err != nil {
				if ctx.Err() == nil {
					logger.WarnContext(ctx, "delete expired push marks failed", "error", err)
				}
				break
			}
			if deleted < markBatch {
				break
			}
		}
	}
}
