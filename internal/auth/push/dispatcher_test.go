package push

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/eventbus"
)

// stubProvider records messages and answers per token.
type stubProvider struct {
	mu      sync.Mutex
	sent    []Message
	answers map[string]error
}

func (p *stubProvider) Send(_ context.Context, m Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, m)
	return p.answers[m.Token]
}

func (p *stubProvider) messages() []Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Message(nil), p.sent...)
}

type stubDevices struct {
	mu      sync.Mutex
	tokens  map[string][]string
	deleted []string
	err     error
}

func (d *stubDevices) PushTargets(_ context.Context, userID string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tokens[userID], d.err
}

func (d *stubDevices) DeletePushToken(_ context.Context, token string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deleted = append(d.deleted, token)
	return nil
}

type harness struct {
	d        *Dispatcher
	provider *stubProvider
	devices  *stubDevices
	now      time.Time
}

func newHarness(t *testing.T, cfg Config) *harness {
	t.Helper()
	h := &harness{provider: &stubProvider{answers: map[string]error{}}, devices: &stubDevices{tokens: map[string][]string{}},
		now: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	h.d = NewDispatcher(h.provider, h.devices, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)
	h.d.now = func() time.Time { return h.now }
	return h
}

// drain sends everything that is ready, as the workers of Run would.
func (h *harness) drain() {
	for {
		select {
		case key := <-h.d.ready:
			h.d.send(context.Background(), key)
		default:
			return
		}
	}
}

func (h *harness) advance(d time.Duration) {
	h.now = h.now.Add(d)
	h.d.sweep()
	h.drain()
}

func TestDispatcherCollapsesSignalsPerUserAndRoom(t *testing.T) {
	h := newHarness(t, Config{Window: 10 * time.Second})
	alice, bob, room, other := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	h.devices.tokens[alice] = []string{"alice-phone", "alice-tablet"}
	h.devices.tokens[bob] = []string{"bob-phone"}

	h.d.Signal(eventbus.PushSignal{RoomID: room, UserIDs: []string{alice, bob}})
	h.drain()
	require.ElementsMatch(t, []Message{{"alice-phone", room}, {"alice-tablet", room}, {"bob-phone", room}}, h.provider.messages(),
		"the first signal is sent at once to every device")

	// A busy room: many messages within the window become one push after it.
	for range 50 {
		h.d.Signal(eventbus.PushSignal{RoomID: room, UserIDs: []string{alice}})
	}
	h.d.Signal(eventbus.PushSignal{RoomID: other, UserIDs: []string{alice}})
	h.drain()
	require.Len(t, h.provider.messages(), 5, "another room is not collapsed with the first")
	h.advance(9 * time.Second)
	require.Len(t, h.provider.messages(), 5)
	h.advance(time.Second)
	require.Len(t, h.provider.messages(), 7, "merged signals are sent once at the end of the window")
	require.Equal(t, Message{"alice-tablet", room}, h.provider.messages()[6])

	// Quiet pairs are forgotten, so the next signal goes out at once.
	h.advance(10 * time.Second)
	h.advance(10 * time.Second)
	require.Zero(t, h.d.Stats().Pending)
	h.d.Signal(eventbus.PushSignal{RoomID: room, UserIDs: []string{bob}})
	h.drain()
	require.Len(t, h.provider.messages(), 8)
}

func TestDispatcherDeletesOnlyInvalidTokens(t *testing.T) {
	h := newHarness(t, Config{})
	user, room := uuid.NewString(), uuid.NewString()
	h.devices.tokens[user] = []string{"gone", "flaky", "fine"}
	h.provider.answers["gone"] = ErrTokenInvalid
	h.provider.answers["flaky"] = errors.New("provider unavailable")
	h.d.Signal(eventbus.PushSignal{RoomID: room, UserIDs: []string{user}})
	h.drain()
	require.Equal(t, []string{"gone"}, h.devices.deleted)
	require.Equal(t, Stats{Pending: 1, Sent: 1, Failed: 1, Invalid: 1}, h.d.Stats())
}

func TestDispatcherSurvivesTokenStoreOutage(t *testing.T) {
	h := newHarness(t, Config{Window: time.Second})
	user, room := uuid.NewString(), uuid.NewString()
	h.devices.err = errors.New("database unavailable")
	h.d.Signal(eventbus.PushSignal{RoomID: room, UserIDs: []string{user}})
	h.drain()
	require.Empty(t, h.provider.messages())
	require.EqualValues(t, 1, h.d.Stats().Failed)

	h.devices.err = nil
	h.devices.tokens[user] = []string{"phone"}
	h.advance(time.Second)
	h.d.Signal(eventbus.PushSignal{RoomID: room, UserIDs: []string{user}})
	h.advance(time.Second)
	require.Equal(t, []Message{{"phone", room}}, h.provider.messages(), "later signals are delivered")
}

func TestDispatcherBoundsPendingPairs(t *testing.T) {
	h := newHarness(t, Config{MaxPending: 3})
	room := uuid.NewString()
	users := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	h.d.Signal(eventbus.PushSignal{RoomID: room, UserIDs: users})
	require.Equal(t, Stats{Pending: 3, Dropped: 2}, h.d.Stats())
	require.Len(t, h.d.ready, 3, "the queue never holds more than the bound")
}

func TestDispatcherRunsConcurrentlyAndStops(t *testing.T) {
	d := NewDispatcher(&stubProvider{answers: map[string]error{}}, &stubDevices{tokens: map[string][]string{}},
		slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Window: 20 * time.Millisecond, Workers: 4})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	var signals sync.WaitGroup
	for range 8 {
		signals.Go(func() {
			for range 100 {
				d.Signal(eventbus.PushSignal{RoomID: uuid.NewString(), UserIDs: []string{uuid.NewString()}})
			}
		})
	}
	signals.Wait()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}
