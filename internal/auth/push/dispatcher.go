// Package push turns push signals from Chat into wake-up messages for the
// devices of their recipients (docs/push.md). A message carries only a room
// id; clients fetch what is new over the authenticated API.
package push

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// ErrTokenInvalid is returned by a Provider when the device token is no
// longer registered with the provider; the token is then deleted.
var ErrTokenInvalid = errors.New("push: device token is no longer valid")

// ErrTemporary marks a Provider failure worth retrying: the network, the
// provider's own errors and its rate limits. Other failures are final for
// that device.
var ErrTemporary = errors.New("push: temporary failure")

// Message is one wake-up signal for one device. TTL is how long the
// provider may hold it for an offline device.
type Message struct {
	Token  string
	RoomID string
	TTL    time.Duration
}

// Provider delivers messages to a push service, obeying the context deadline.
type Provider interface {
	Send(ctx context.Context, message Message) error
}

// Devices is Auth's token store: the tokens of a user's active logins.
type Devices interface {
	PushTargets(ctx context.Context, userID string) ([]string, error)
	// IsPushTarget reports whether token still belongs to an active login of
	// userID. It is the authorization of each provider request: the tokens
	// PushTargets returned are only candidates.
	IsPushTarget(ctx context.Context, userID, token string) (bool, error)
	DeletePushToken(ctx context.Context, token string) error
}

// Marks records which recipients of a stored signal were handled, so that a
// redelivery of the signal skips them (ADR 0003).
type Marks interface {
	// UnmarkedPushRecipients returns those of userIDs without a mark for
	// the signal, in their order.
	UnmarkedPushRecipients(ctx context.Context, signalID string, userIDs []string) ([]string, error)
	// MarkPushRecipient records userID as handled for every signal in
	// signalIDs until expires.
	MarkPushRecipient(ctx context.Context, signalIDs []string, userID string, expires time.Time) error
}

// Config bounds the dispatcher; zero fields take the defaults.
type Config struct {
	// Window is the shortest time between two pushes for one user and room.
	// Signals inside it are merged into one push at its end.
	Window time.Duration
	// MaxPending bounds the user/room pairs tracked at once.
	MaxPending int
	// Workers is the number of concurrent sends.
	Workers int
	// SendTimeout bounds loading the tokens of one user, sending to them
	// and recording the result.
	SendTimeout time.Duration
	// MarkTTL is how long a recipient's mark is kept; it must outlive the
	// signal in the stream.
	MarkTTL time.Duration
}

const (
	defaultWindow      = 10 * time.Second
	defaultMaxPending  = 10000
	defaultWorkers     = 4
	defaultSendTimeout = 10 * time.Second
	defaultMarkTTL     = 2 * time.Hour
)

// Delivery is one stored signal in work: the recipients still to be woken
// and how to finish it.
type Delivery struct {
	// ID is the signal's Nats-Msg-Id.
	ID     string
	RoomID string
	// UserIDs are the recipients without a mark.
	UserIDs []string
	// Expires is the end of the signal's life: a push for it is held by the
	// provider no longer than until then.
	Expires time.Time
	// Done is called once, without dispatcher locks held, when every
	// recipient was handled. retry reports that at least one failed
	// temporarily and has no mark.
	Done func(retry bool)
}

// work tracks the recipients of a Delivery not yet handled.
type work struct {
	*Delivery
	outstanding int
	retry       bool
}

type target struct{ userID, roomID string }

// entry is the collapsing state of one user and room. sending holds the
// deliveries the send in flight answers for; waiting holds those merged
// since, sent at the end of the window.
type entry struct {
	sentAt   time.Time
	inFlight bool
	sending  []*work
	waiting  []*work
}

// Dispatcher collapses signals per user and room and sends them through a
// Provider. Submit never blocks; Run does the work.
type Dispatcher struct {
	provider Provider
	devices  Devices
	marks    Marks
	logger   *slog.Logger
	cfg      Config
	now      func() time.Time

	mu      sync.Mutex
	pending map[target]*entry
	ready   chan target

	sent, failed, dropped, invalid, malformed, skipped, expired atomic.Uint64
	lastWarn                                                    atomic.Int64
}

// NewDispatcher creates a dispatcher; call Run to start it.
func NewDispatcher(provider Provider, devices Devices, marks Marks, logger *slog.Logger, cfg Config) *Dispatcher {
	if cfg.Window <= 0 {
		cfg.Window = defaultWindow
	}
	if cfg.MaxPending <= 0 {
		cfg.MaxPending = defaultMaxPending
	}
	if cfg.Workers <= 0 {
		cfg.Workers = defaultWorkers
	}
	if cfg.SendTimeout <= 0 {
		cfg.SendTimeout = defaultSendTimeout
	}
	if cfg.MarkTTL <= 0 {
		cfg.MarkTTL = defaultMarkTTL
	}
	return &Dispatcher{provider: provider, devices: devices, marks: marks, logger: logger, cfg: cfg, now: time.Now,
		pending: make(map[target]*entry), ready: make(chan target, cfg.MaxPending)}
}

// Free is how many new user/room pairs Submit can take now. The consumer
// fetches a signal only when its recipients fit, so none is dropped.
func (d *Dispatcher) Free() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cfg.MaxPending - len(d.pending)
}

// Submit schedules a push to each recipient of delivery: now if none was
// sent for that room recently, otherwise once at the end of the window. A
// recipient that does not fit is failed for a retry.
func (d *Dispatcher) Submit(delivery *Delivery) {
	w := &work{Delivery: delivery, outstanding: len(delivery.UserIDs)}
	var finished []*work
	d.mu.Lock()
	for _, user := range delivery.UserIDs {
		key := target{user, delivery.RoomID}
		if e, ok := d.pending[key]; ok {
			e.waiting = append(e.waiting, w)
			continue
		}
		if len(d.pending) >= d.cfg.MaxPending {
			d.dropped.Add(1)
			finished = d.resolve(finished, w, true)
			continue
		}
		d.pending[key] = &entry{inFlight: true, sending: []*work{w}}
		d.ready <- key // cannot block: at most MaxPending keys are queued
	}
	if len(delivery.UserIDs) == 0 {
		finished = append(finished, w)
	}
	d.mu.Unlock()
	finish(finished)
}

// resolve counts one recipient of w as handled; a finished w is appended.
// The caller holds d.mu.
func (d *Dispatcher) resolve(finished []*work, w *work, retry bool) []*work {
	w.outstanding--
	w.retry = w.retry || retry
	if w.outstanding == 0 {
		finished = append(finished, w)
	}
	return finished
}

func finish(finished []*work) {
	for _, w := range finished {
		w.Done(w.retry)
	}
}

// Run sends until ctx ends, then waits for the sends in progress, each
// bounded by SendTimeout. Deliveries still waiting are not finished; their
// consumer returns them to the stream.
func (d *Dispatcher) Run(ctx context.Context) {
	var workers sync.WaitGroup
	for range d.cfg.Workers {
		workers.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case key := <-d.ready:
					if ctx.Err() != nil {
						return
					}
					d.send(ctx, key)
				}
			}
		})
	}
	tick := time.NewTicker(max(d.cfg.Window/10, 10*time.Millisecond))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			workers.Wait()
			return
		case <-tick.C:
			d.sweep()
		}
	}
}

// sweep ends the windows that ran out: a pair with merged signals is sent
// again, an idle one is forgotten.
func (d *Dispatcher) sweep() {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	for key, e := range d.pending {
		if e.inFlight || now.Sub(e.sentAt) < d.cfg.Window {
			continue
		}
		if len(e.waiting) == 0 {
			delete(d.pending, key)
			continue
		}
		e.sending, e.waiting, e.inFlight = e.waiting, nil, true
		d.ready <- key
	}
}

func (d *Dispatcher) send(ctx context.Context, key target) {
	d.mu.Lock()
	sending := d.pending[key].sending
	d.mu.Unlock()
	// A send in progress finishes within its own deadline even when Run is
	// stopped, so shutdown does not turn it into a failure.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), d.cfg.SendTimeout)
	defer cancel()
	retry := d.wake(ctx, key, sending)
	if !retry {
		ids := make([]string, len(sending))
		for i, w := range sending {
			ids[i] = w.ID
		}
		if err := d.marks.MarkPushRecipient(ctx, ids, key.userID, d.now().Add(d.cfg.MarkTTL)); err != nil {
			// The push went out, but a redelivery will repeat it.
			d.failed.Add(1)
			d.warn(ctx, "record push recipient failed", err)
			retry = true
		}
	}
	var finished []*work
	d.mu.Lock()
	e := d.pending[key]
	e.inFlight, e.sentAt, e.sending = false, d.now(), nil
	for _, w := range sending {
		finished = d.resolve(finished, w, retry)
	}
	d.mu.Unlock()
	finish(finished)
}

// wake sends the pair's push to every active device of the user and
// reports whether it must be retried. The provider may hold it until the
// latest of the merged signals expires; when all have, nothing is sent.
func (d *Dispatcher) wake(ctx context.Context, key target, sending []*work) (retry bool) {
	var expires time.Time
	for _, w := range sending {
		if w.Expires.After(expires) {
			expires = w.Expires
		}
	}
	ttl := expires.Sub(d.now())
	if ttl <= 0 {
		d.expired.Add(1)
		return false
	}
	tokens, err := d.devices.PushTargets(ctx, key.userID)
	if err != nil {
		d.failed.Add(1)
		d.warn(ctx, "load push targets failed", err)
		return true
	}
	for _, token := range tokens {
		// The list is a snapshot, and sends to earlier devices take time:
		// a login revoked or a token moved to another user meanwhile must
		// not get a send started. Each request is checked right before it
		// starts; when the check fails, nothing more is sent.
		active, err := d.devices.IsPushTarget(ctx, key.userID, token)
		if err != nil {
			d.failed.Add(1)
			d.warn(ctx, "check push target failed", err)
			return true
		}
		if !active {
			d.skipped.Add(1)
			continue
		}
		err = d.provider.Send(ctx, Message{Token: token, RoomID: key.roomID, TTL: ttl})
		switch {
		case err == nil:
			d.sent.Add(1)
		case errors.Is(err, ErrTokenInvalid):
			d.invalid.Add(1)
			if err := d.devices.DeletePushToken(ctx, token); err != nil {
				d.warn(ctx, "delete invalid push token failed", err)
			}
		case errors.Is(err, ErrTemporary) || ctx.Err() != nil:
			// The other devices still get theirs; the retry repeats the
			// push to all of them, which a client's sync makes harmless.
			d.failed.Add(1)
			d.warn(ctx, "push send failed", err)
			retry = true
		default:
			// A final answer for this device, such as a rejected request:
			// retrying would get the same.
			d.failed.Add(1)
			d.warn(ctx, "push send rejected", err)
		}
	}
	return retry
}

// warnInterval limits failure warnings; counters keep the totals.
const warnInterval = 10 * time.Second

// warn logs at most one failure per warnInterval, so an outage of the
// provider or database cannot flood the log.
func (d *Dispatcher) warn(ctx context.Context, message string, err error) {
	now := d.now().UnixNano()
	last := d.lastWarn.Load()
	if (last != 0 && now-last < int64(warnInterval)) || !d.lastWarn.CompareAndSwap(last, now) {
		return
	}
	d.logger.WarnContext(ctx, message, "error", err, "failed_total", d.failed.Load())
}

// Stats is an approximate view for metrics. Skipped counts sends not started
// because the device left the user's active logins after the tokens were
// loaded; Expired counts pushes not sent because their signals outlived
// their hour.
type Stats struct {
	Pending                                                     int
	Sent, Failed, Dropped, Invalid, Malformed, Skipped, Expired uint64
}

// Stats returns counters since start.
func (d *Dispatcher) Stats() Stats {
	d.mu.Lock()
	pending := len(d.pending)
	d.mu.Unlock()
	return Stats{Pending: pending, Sent: d.sent.Load(), Failed: d.failed.Load(), Dropped: d.dropped.Load(), Invalid: d.invalid.Load(),
		Malformed: d.malformed.Load(), Skipped: d.skipped.Load(), Expired: d.expired.Load()}
}
