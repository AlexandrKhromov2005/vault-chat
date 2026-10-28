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

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/eventbus"
)

// ErrTokenInvalid is returned by a Provider when the device token is no
// longer registered with the provider; the token is then deleted.
var ErrTokenInvalid = errors.New("push: device token is no longer valid")

// Message is one wake-up signal for one device.
type Message struct {
	Token  string
	RoomID string
}

// Provider delivers messages to a push service, obeying the context deadline.
type Provider interface {
	Send(ctx context.Context, message Message) error
}

// Devices is Auth's token store: the tokens of a user's active logins.
type Devices interface {
	PushTargets(ctx context.Context, userID string) ([]string, error)
	DeletePushToken(ctx context.Context, token string) error
}

// Config bounds the dispatcher; zero fields take the defaults.
type Config struct {
	// Window is the shortest time between two pushes for one user and room.
	// Signals inside it are merged into one push at its end.
	Window time.Duration
	// MaxPending bounds the user/room pairs tracked at once; signals for new
	// pairs beyond it are dropped.
	MaxPending int
	// Workers is the number of concurrent sends.
	Workers int
	// SendTimeout bounds loading the tokens of one user and sending to them.
	SendTimeout time.Duration
}

const (
	defaultWindow      = 10 * time.Second
	defaultMaxPending  = 10000
	defaultWorkers     = 4
	defaultSendTimeout = 10 * time.Second
)

type target struct{ userID, roomID string }

// entry is the collapsing state of one user and room.
type entry struct {
	sentAt   time.Time
	inFlight bool
	dirty    bool
}

// Dispatcher collapses signals per user and room and sends them through a
// Provider. Signal never blocks; Run does the work.
type Dispatcher struct {
	provider Provider
	devices  Devices
	logger   *slog.Logger
	cfg      Config
	now      func() time.Time

	mu      sync.Mutex
	pending map[target]*entry
	ready   chan target

	sent, failed, dropped, invalid, malformed atomic.Uint64
	lastWarn                                  atomic.Int64
}

// NewDispatcher creates a dispatcher; call Run to start it.
func NewDispatcher(provider Provider, devices Devices, logger *slog.Logger, cfg Config) *Dispatcher {
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
	return &Dispatcher{provider: provider, devices: devices, logger: logger, cfg: cfg, now: time.Now,
		pending: make(map[target]*entry), ready: make(chan target, cfg.MaxPending)}
}

// Signal schedules a push to each recipient of signal: now if none was sent
// for that room recently, otherwise once at the end of the window.
func (d *Dispatcher) Signal(signal eventbus.PushSignal) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, user := range signal.UserIDs {
		key := target{user, signal.RoomID}
		if e, ok := d.pending[key]; ok {
			e.dirty = true
			continue
		}
		if len(d.pending) >= d.cfg.MaxPending {
			d.dropped.Add(1)
			continue
		}
		d.pending[key] = &entry{inFlight: true}
		d.ready <- key // cannot block: at most MaxPending keys are queued
	}
}

// Run sends until ctx ends, then waits for the sends in progress, each
// bounded by SendTimeout.
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
		if !e.dirty {
			delete(d.pending, key)
			continue
		}
		e.dirty, e.inFlight = false, true
		d.ready <- key
	}
}

func (d *Dispatcher) send(ctx context.Context, key target) {
	defer func() {
		d.mu.Lock()
		if e, ok := d.pending[key]; ok {
			e.inFlight, e.sentAt = false, d.now()
		}
		d.mu.Unlock()
	}()
	// A send in progress finishes within its own deadline even when Run is
	// stopped, so shutdown does not turn it into a failure.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), d.cfg.SendTimeout)
	defer cancel()
	tokens, err := d.devices.PushTargets(ctx, key.userID)
	if err != nil {
		d.failed.Add(1)
		d.warn(ctx, "load push targets failed", err)
		return
	}
	for _, token := range tokens {
		err := d.provider.Send(ctx, Message{Token: token, RoomID: key.roomID})
		switch {
		case err == nil:
			d.sent.Add(1)
		case errors.Is(err, ErrTokenInvalid):
			d.invalid.Add(1)
			if err := d.devices.DeletePushToken(ctx, token); err != nil {
				d.warn(ctx, "delete invalid push token failed", err)
			}
		default:
			// Best effort: the next signal or the client's own sync recovers.
			d.failed.Add(1)
			d.warn(ctx, "push send failed", err)
		}
	}
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

// Stats is an approximate view for metrics.
type Stats struct {
	Pending                                   int
	Sent, Failed, Dropped, Invalid, Malformed uint64
}

// Stats returns counters since start.
func (d *Dispatcher) Stats() Stats {
	d.mu.Lock()
	pending := len(d.pending)
	d.mu.Unlock()
	return Stats{Pending: pending, Sent: d.sent.Load(), Failed: d.failed.Load(), Dropped: d.dropped.Load(), Invalid: d.invalid.Load(),
		Malformed: d.malformed.Load()}
}
