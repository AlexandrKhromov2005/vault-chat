package push

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/eventbus"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/pushbus"
)

// ConsumerConfig tunes a Consumer; zero fields take the defaults.
type ConsumerConfig struct {
	// RetryDelays are the delays before the redeliveries of a signal that
	// failed temporarily, by the number of the failed delivery. A failure
	// of a later delivery terminates the signal; with pushbus.MaxDeliver
	// deliveries there are four delays.
	RetryDelays []time.Duration
	// Progress is how often a signal in work extends its acknowledgement
	// deadline, so that one waiting in the collapsing window or for a slow
	// provider is not redelivered. It must be well inside pushbus.AckWait.
	Progress time.Duration
}

// DefaultRetryDelays are the delays of ADR 0003: 5 s, 30 s, 2 min, 10 min.
var DefaultRetryDelays = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute}

const (
	defaultProgress = 10 * time.Second
	// fetchWait bounds a pull, and with it how long a stop waits.
	fetchWait    = time.Second
	capacityPoll = 100 * time.Millisecond
)

// Source yields stored signals; a jetstream.Consumer is one.
type Source interface {
	Next(opts ...jetstream.FetchOpt) (jetstream.Msg, error)
}

// Consumer feeds stored push signals to a Dispatcher and acknowledges each
// when all its recipients are handled (docs/adr/0003-push-signals-jetstream.md).
type Consumer struct {
	source Source
	d      *Dispatcher
	marks  Marks
	logger *slog.Logger
	cfg    ConsumerConfig
	now    func() time.Time

	mu     sync.Mutex
	inWork map[jetstream.Msg]struct{}

	received, acked, retried, terminated, exhausted, expired atomic.Uint64
	lastWarn                                                 atomic.Int64
}

// NewConsumer binds a source of signals to d. Marks are the same store d
// records into.
func NewConsumer(source Source, d *Dispatcher, marks Marks, logger *slog.Logger, cfg ConsumerConfig) *Consumer {
	if len(cfg.RetryDelays) == 0 {
		cfg.RetryDelays = DefaultRetryDelays
	}
	if cfg.Progress <= 0 {
		cfg.Progress = defaultProgress
	}
	return &Consumer{source: source, d: d, marks: marks, logger: logger, cfg: cfg, now: time.Now, inWork: map[jetstream.Msg]struct{}{}}
}

// Run fetches signals until ctx ends. A signal is fetched only when the
// dispatcher has room for all its recipients and fewer than
// pushbus.MaxAckPending are in work. On return, the signals still in work
// are given back for immediate redelivery; recipients already handled keep
// their marks.
func (c *Consumer) Run(ctx context.Context) {
	// Progress has its own ticker: a pull takes about as long as fetchWait
	// whatever it is given, and must not delay the extensions.
	var progress sync.WaitGroup
	progressCtx, stopProgress := context.WithCancel(ctx)
	progress.Go(func() {
		tick := time.NewTicker(c.cfg.Progress)
		defer tick.Stop()
		for {
			select {
			case <-progressCtx.Done():
				return
			case <-tick.C:
				c.progress()
			}
		}
	})
	defer func() {
		stopProgress()
		progress.Wait()
		c.giveBack()
	}()
	for ctx.Err() == nil {
		if c.d.Free() < eventbus.MaxPushSignalRecipients || c.working() >= pushbus.MaxAckPending {
			select {
			case <-ctx.Done():
			case <-time.After(capacityPoll):
			}
			continue
		}
		msg, err := c.source.Next(jetstream.FetchMaxWait(fetchWait))
		if err != nil {
			if !errors.Is(err, jetstream.ErrNoMessages) && !errors.Is(err, context.DeadlineExceeded) {
				c.warn(ctx, "fetch push signal failed", err)
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
				}
			}
			continue
		}
		c.handle(ctx, msg)
	}
}

func (c *Consumer) working() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.inWork)
}

// handle checks one stored signal and hands its unhandled recipients to the
// dispatcher. Anyone with publish permission could have written it.
func (c *Consumer) handle(ctx context.Context, msg jetstream.Msg) {
	c.received.Add(1)
	meta, err := msg.Metadata()
	if err != nil {
		c.terminate(ctx, msg, "push signal without metadata terminated")
		return
	}
	id := first(msg.Headers().Values(jetstream.MsgIDHeader))
	signal, err := eventbus.DecodePushSignal(msg.Data())
	if err != nil || !pushbus.ValidSignalID(id) {
		c.d.malformed.Add(1)
		c.terminate(ctx, msg, "malformed push signal terminated")
		return
	}
	expires := meta.Timestamp.Add(pushbus.MaxAge)
	if !c.now().Before(expires) {
		c.expired.Add(1)
		c.ack(ctx, msg)
		return
	}
	users, err := c.marks.UnmarkedPushRecipients(ctx, id, signal.UserIDs)
	if err != nil {
		c.warn(ctx, "load push marks failed", err)
		c.retry(ctx, msg, meta)
		return
	}
	if len(users) == 0 {
		c.ack(ctx, msg)
		return
	}
	c.mu.Lock()
	c.inWork[msg] = struct{}{}
	c.mu.Unlock()
	c.d.Submit(&Delivery{ID: id, RoomID: signal.RoomID, UserIDs: users, Expires: expires, Done: func(retry bool) {
		c.mu.Lock()
		_, ok := c.inWork[msg]
		delete(c.inWork, msg)
		c.mu.Unlock()
		if !ok {
			return // given back on shutdown
		}
		if retry {
			c.retry(ctx, msg, meta)
			return
		}
		c.ack(ctx, msg)
	}})
}

func first(values []string) string {
	if len(values) != 1 {
		return ""
	}
	return values[0]
}

func (c *Consumer) ack(ctx context.Context, msg jetstream.Msg) {
	if err := msg.Ack(); err != nil {
		// Redelivered after AckWait; the marks make it a no-op.
		c.warn(ctx, "acknowledge push signal failed", err)
		return
	}
	c.acked.Add(1)
}

// retry returns a signal after the delay for its delivery number, or
// terminates it after the last delivery.
func (c *Consumer) retry(ctx context.Context, msg jetstream.Msg, meta *jetstream.MsgMetadata) {
	n := int(meta.NumDelivered)
	if n < 1 || n > len(c.cfg.RetryDelays) {
		c.exhausted.Add(1)
		c.terminate(ctx, msg, "push signal exhausted its deliveries")
		return
	}
	if err := msg.NakWithDelay(c.cfg.RetryDelays[n-1]); err != nil {
		c.warn(ctx, "return push signal failed", err)
		return
	}
	c.retried.Add(1)
}

func (c *Consumer) terminate(ctx context.Context, msg jetstream.Msg, reason string) {
	if err := msg.Term(); err != nil {
		c.warn(ctx, "terminate push signal failed", err)
		return
	}
	total := c.terminated.Add(1)
	if c.throttled() {
		return
	}
	c.logger.WarnContext(ctx, reason, "terminated_total", total)
}

// progress extends the acknowledgement deadline of every signal in work.
func (c *Consumer) progress() {
	c.mu.Lock()
	msgs := make([]jetstream.Msg, 0, len(c.inWork))
	for msg := range c.inWork {
		msgs = append(msgs, msg)
	}
	c.mu.Unlock()
	for _, msg := range msgs {
		_ = msg.InProgress()
	}
}

// giveBack returns the signals in work at once, so that the next run
// delivers them without waiting for AckWait.
func (c *Consumer) giveBack() {
	c.mu.Lock()
	msgs := make([]jetstream.Msg, 0, len(c.inWork))
	for msg := range c.inWork {
		msgs = append(msgs, msg)
	}
	clear(c.inWork)
	c.mu.Unlock()
	for _, msg := range msgs {
		_ = msg.NakWithDelay(0)
	}
}

func (c *Consumer) warn(ctx context.Context, message string, err error) {
	if c.throttled() {
		return
	}
	c.logger.WarnContext(ctx, message, "error", err)
}

func (c *Consumer) throttled() bool {
	now := c.now().UnixNano()
	last := c.lastWarn.Load()
	return (last != 0 && now-last < int64(warnInterval)) || !c.lastWarn.CompareAndSwap(last, now)
}

// ConsumerStats counts deliveries of stored signals since start and their
// outcomes: Exhausted ones were terminated after their last delivery,
// Expired ones acknowledged unsent after their hour.
type ConsumerStats struct {
	InWork                                                   int
	Received, Acked, Retried, Terminated, Exhausted, Expired uint64
}

// Stats returns counters since start.
func (c *Consumer) Stats() ConsumerStats {
	return ConsumerStats{InWork: c.working(), Received: c.received.Load(), Acked: c.acked.Load(), Retried: c.retried.Load(), Terminated: c.terminated.Load(),
		Exhausted: c.exhausted.Load(), Expired: c.expired.Load()}
}
