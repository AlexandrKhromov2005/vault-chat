// Package events publishes committed Chat changes to Redis Pub/Sub, from
// where every Gateway instance relays them to its WebSocket clients.
package events

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/eventbus"
)

const (
	// DefaultQueueSize is the number of changes waiting for Redis before new
	// ones are dropped.
	DefaultQueueSize = 1024
	// DefaultPublishTimeout bounds one Redis round trip of the worker.
	DefaultPublishTimeout = time.Second
	// dropLogInterval limits warnings about dropped changes while Redis is
	// down or too slow; the total is kept in Dropped.
	dropLogInterval = 10 * time.Second
)

// Pipeliner is the part of the Redis client the publisher needs.
type Pipeliner interface {
	Pipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error)
}

// publication is one encoded event and the users it is published to.
type publication struct {
	kind       domain.EventKind
	roomID     string
	payload    []byte
	recipients []string
}

// change holds the publications of one committed change and the context of
// the request that made it, without its cancellation, for logging.
type change struct {
	ctx          context.Context
	publications []publication
}

// Publisher sends each event to the channel of every recipient. Publishing is
// best effort: the change has already committed, so requests only enqueue it
// and never wait for Redis. One worker publishes the queue in order, each
// change in one pipeline. A failure is logged, a full queue drops the change,
// and clients recover what they missed through message sequences and catch-up.
type Publisher struct {
	redis   Pipeliner
	logger  *slog.Logger
	timeout time.Duration
	queue   chan change
	// base is canceled when draining on Close runs out of time.
	base    context.Context
	abort   context.CancelFunc
	stopped chan struct{}

	mu     sync.RWMutex
	closed bool

	failed           atomic.Uint64
	published        atomic.Uint64
	encodingFailures atomic.Uint64
	dropped          atomic.Uint64
	lastDropLog      atomic.Int64
}

// Option configures a Publisher.
type Option func(*Publisher)

// WithQueueSize sets how many changes may wait for Redis; nonpositive sizes
// keep the default.
func WithQueueSize(size int) Option {
	return func(p *Publisher) {
		if size > 0 {
			p.queue = make(chan change, size)
		}
	}
}

// WithPublishTimeout bounds each Redis round trip; nonpositive durations keep
// the default.
func WithPublishTimeout(timeout time.Duration) Option {
	return func(p *Publisher) {
		if timeout > 0 {
			p.timeout = timeout
		}
	}
}

// NewPublisher binds a Redis client and the logger for publishing failures and
// starts the worker. Close stops it.
func NewPublisher(redis Pipeliner, logger *slog.Logger, options ...Option) *Publisher {
	base, abort := context.WithCancel(context.Background())
	p := &Publisher{redis: redis, logger: logger, timeout: DefaultPublishTimeout,
		queue: make(chan change, DefaultQueueSize), base: base, abort: abort, stopped: make(chan struct{})}
	for _, option := range options {
		option(p)
	}
	go p.run()
	return p
}

// NewRedisClient returns a client whose short timeouts keep an unreachable
// Redis from holding the worker, and with it the queue, for long.
func NewRedisClient(addr, password string) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:                  addr,
		Password:              password,
		ContextTimeoutEnabled: true,
		DialTimeout:           250 * time.Millisecond,
		ReadTimeout:           250 * time.Millisecond,
		WriteTimeout:          250 * time.Millisecond,
		PoolTimeout:           500 * time.Millisecond,
	})
}

// Notify enqueues the events of one committed change without waiting. It
// ignores the caller's cancellation, since the change committed even if the
// client left. Events without recipients are skipped; when the queue is full
// or the publisher is closed the change is dropped.
func (p *Publisher) Notify(ctx context.Context, events ...domain.Event) {
	publications := make([]publication, 0, len(events))
	for _, event := range events {
		if len(event.Recipients) == 0 {
			continue
		}
		payload, err := Encode(event)
		if err != nil {
			p.encodingFailures.Add(1)
			p.logger.ErrorContext(ctx, "encode chat event failed", "kind", event.Kind, "error", err)
			continue
		}
		publications = append(publications, publication{kind: event.Kind, roomID: event.RoomID, payload: payload,
			recipients: slices.Clone(event.Recipients)})
	}
	if len(publications) == 0 {
		return
	}
	queued := change{ctx: context.WithoutCancel(ctx), publications: publications}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		p.drop(queued, "publisher closed")
		return
	}
	select {
	case p.queue <- queued:
	default:
		p.drop(queued, "queue full")
	}
}

// Dropped returns how many events were dropped because the queue was full or
// the publisher was closed.
func (p *Publisher) Dropped() uint64 { return p.dropped.Load() }

// Close stops accepting events and waits until the queued ones are published
// or ctx ends. After ctx ends the publication in progress is aborted, the rest
// of the queue is dropped and ctx's error is returned. Close may be called
// more than once.
func (p *Publisher) Close(ctx context.Context) error {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		close(p.queue)
	}
	p.mu.Unlock()
	select {
	case <-p.stopped:
		return nil
	case <-ctx.Done():
	}
	p.abort()
	<-p.stopped
	return fmt.Errorf("drain chat events: %w", ctx.Err())
}

func (p *Publisher) run() {
	defer close(p.stopped)
	defer p.abort()
	for queued := range p.queue {
		p.publish(queued)
	}
}

// publish sends one change to the channels of all its recipients in one round trip.
func (p *Publisher) publish(queued change) {
	if p.base.Err() != nil {
		p.drop(queued, "shutdown timed out")
		return
	}
	ctx, cancel := context.WithTimeout(p.base, p.timeout)
	defer cancel()
	_, err := p.redis.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, publication := range queued.publications {
			for _, user := range publication.recipients {
				pipe.Publish(ctx, eventbus.UserChannel(user), publication.payload)
			}
		}
		return nil
	})
	if err == nil {
		p.published.Add(uint64(len(queued.publications)))
		return
	}
	if p.base.Err() != nil {
		p.drop(queued, "shutdown timed out")
		return
	}
	p.failed.Add(uint64(len(queued.publications)))
	first := queued.publications[0]
	p.logger.WarnContext(queued.ctx, "publish chat events failed", "kind", first.kind, "room_id", first.roomID,
		"events", len(queued.publications), "error", err)
}

// drop counts the events of a change that will not be published and logs at
// most one warning per dropLogInterval.
func (p *Publisher) drop(queued change, reason string) {
	total := p.dropped.Add(uint64(len(queued.publications)))
	now := time.Now().UnixNano()
	last := p.lastDropLog.Load()
	if (last != 0 && now-last < int64(dropLogInterval)) || !p.lastDropLog.CompareAndSwap(last, now) {
		return
	}
	p.logger.WarnContext(queued.ctx, "chat events dropped", "reason", reason, "dropped_total", total)
}

// Encode converts an event to its protobuf wire form, chatv1.Event.
func Encode(event domain.Event) ([]byte, error) {
	wire := &chatv1.Event{}
	change := &chatv1.MemberChange{RoomId: event.RoomID, UserId: event.UserID}
	switch event.Kind {
	case domain.MessageCreated:
		m := event.Message
		if m == nil {
			return nil, fmt.Errorf("message event without a message")
		}
		wire.Kind = &chatv1.Event_MessageCreated{MessageCreated: &chatv1.Message{Id: m.ID, RoomId: m.RoomID, SenderId: m.SenderID,
			ClientMessageId: m.ClientID, Text: m.Text, CreatedAt: timestamppb.New(m.CreatedAt), Sequence: m.Sequence, AttachmentIds: m.AttachmentIDs}}
	case domain.MemberAdded:
		wire.Kind = &chatv1.Event_MemberAdded{MemberAdded: change}
	case domain.MemberRemoved:
		wire.Kind = &chatv1.Event_MemberRemoved{MemberRemoved: change}
	case domain.MemberLeft:
		wire.Kind = &chatv1.Event_MemberLeft{MemberLeft: change}
	default:
		return nil, fmt.Errorf("unknown event kind %q", event.Kind)
	}
	payload, err := proto.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}
	return payload, nil
}

// Stats is a race-safe, approximate scrape-time view of the event publisher.
type Stats struct {
	Pending, Capacity                            int
	Dropped, Failed, Published, EncodingFailures uint64
}

// Stats distinguishes queue/shutdown losses from ambiguous Redis failures.
func (p *Publisher) Stats() Stats {
	return Stats{Pending: len(p.queue), Capacity: cap(p.queue), Dropped: p.dropped.Load(), Failed: p.failed.Load(), Published: p.published.Load(), EncodingFailures: p.encodingFailures.Load()}
}
