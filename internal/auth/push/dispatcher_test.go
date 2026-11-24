package push

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// stubProvider records messages and answers per token. onSend, if set, runs
// inside the call, as a provider request in progress.
type stubProvider struct {
	mu      sync.Mutex
	sent    []Message
	answers map[string]error
	onSend  func(Message)
}

func (p *stubProvider) Send(_ context.Context, m Message) error {
	if p.onSend != nil {
		p.onSend(m)
	}
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

// pushes are the messages without their TTL, for comparison.
func (p *stubProvider) pushes() []Message {
	messages := p.messages()
	for i := range messages {
		messages[i].TTL = 0
	}
	return messages
}

type stubDevices struct {
	mu        sync.Mutex
	tokens    map[string][]string
	deleted   []string
	err       error
	targetErr error
}

func (d *stubDevices) PushTargets(_ context.Context, userID string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.tokens[userID]...), d.err
}

func (d *stubDevices) IsPushTarget(_ context.Context, userID, token string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.targetErr != nil {
		return false, d.targetErr
	}
	return slices.Contains(d.tokens[userID], token), nil
}

// revoke removes token from userID's logins, as a committed revocation.
func (d *stubDevices) revoke(userID, token string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.tokens[userID] = slices.DeleteFunc(d.tokens[userID], func(t string) bool { return t == token })
}

func (d *stubDevices) DeletePushToken(_ context.Context, token string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deleted = append(d.deleted, token)
	return nil
}

// memoryMarks is a Marks store in memory; err fails every call.
type memoryMarks struct {
	mu      sync.Mutex
	marks   map[string]time.Time // signal id + user
	err     error
	markErr error
}

func newMemoryMarks() *memoryMarks { return &memoryMarks{marks: map[string]time.Time{}} }

func (m *memoryMarks) UnmarkedPushRecipients(_ context.Context, signalID string, userIDs []string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	var unmarked []string
	for _, user := range userIDs {
		if _, ok := m.marks[signalID+"/"+user]; !ok {
			unmarked = append(unmarked, user)
		}
	}
	return unmarked, nil
}

func (m *memoryMarks) MarkPushRecipient(_ context.Context, signalIDs []string, userID string, expires time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	if m.markErr != nil {
		return m.markErr
	}
	for _, id := range signalIDs {
		m.marks[id+"/"+userID] = expires
	}
	return nil
}

func (m *memoryMarks) has(signalID, userID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.marks[signalID+"/"+userID]
	return ok
}

type harness struct {
	d        *Dispatcher
	provider *stubProvider
	devices  *stubDevices
	marks    *memoryMarks
	now      time.Time
}

func newHarness(t *testing.T, cfg Config) *harness {
	t.Helper()
	h := &harness{provider: &stubProvider{answers: map[string]error{}}, devices: &stubDevices{tokens: map[string][]string{}},
		marks: newMemoryMarks(), now: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	h.d = NewDispatcher(h.provider, h.devices, h.marks, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)
	h.d.now = func() time.Time { return h.now }
	return h
}

// outcome records how a delivery finished.
type outcome struct {
	mu    sync.Mutex
	calls int
	retry bool
}

func (o *outcome) done(retry bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls++
	o.retry = retry
}

// state is "pending", "done" or "retry"; a second Done call fails the test.
func (o *outcome) state(t *testing.T) string {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	require.LessOrEqual(t, o.calls, 1, "Done is called once")
	switch {
	case o.calls == 0:
		return "pending"
	case o.retry:
		return "retry"
	default:
		return "done"
	}
}

var signalCount int

// signal submits a delivery published now with a fresh id.
func (h *harness) signal(room string, users ...string) (*outcome, string) {
	return h.signalPublished(h.now, room, users...)
}

func (h *harness) signalPublished(at time.Time, room string, users ...string) (*outcome, string) {
	signalCount++
	id := fmt.Sprintf("%064x", signalCount)
	o := &outcome{}
	h.d.Submit(&Delivery{ID: id, RoomID: room, UserIDs: users, Expires: at.Add(time.Hour), Done: o.done})
	return o, id
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

	first, _ := h.signal(room, alice, bob)
	h.drain()
	require.ElementsMatch(t, []Message{{Token: "alice-phone", RoomID: room}, {Token: "alice-tablet", RoomID: room}, {Token: "bob-phone", RoomID: room}},
		h.provider.pushes(), "the first signal is sent at once to every device")
	require.Equal(t, "done", first.state(t))

	// A busy room: many messages within the window become one push after it.
	var busy []*outcome
	for range 50 {
		o, _ := h.signal(room, alice)
		busy = append(busy, o)
	}
	h.signal(other, alice)
	h.drain()
	require.Len(t, h.provider.messages(), 5, "another room is not collapsed with the first")
	h.advance(9 * time.Second)
	require.Len(t, h.provider.messages(), 5)
	require.Equal(t, "pending", busy[49].state(t), "a merged signal is not finished before its push")
	h.advance(time.Second)
	require.Len(t, h.provider.messages(), 7, "merged signals are sent once at the end of the window")
	require.Equal(t, Message{Token: "alice-tablet", RoomID: room}, h.provider.pushes()[6])
	for _, o := range busy {
		require.Equal(t, "done", o.state(t))
	}

	// Quiet pairs are forgotten, so the next signal goes out at once.
	h.advance(10 * time.Second)
	h.advance(10 * time.Second)
	require.Zero(t, h.d.Stats().Pending)
	h.signal(room, bob)
	h.drain()
	require.Len(t, h.provider.messages(), 8)
}

// Every signal merged into a push gets a mark for its recipient, and only
// after the push was handled.
func TestDispatcherMarksEveryMergedSignalAfterItsPush(t *testing.T) {
	h := newHarness(t, Config{Window: 10 * time.Second})
	user, room := uuid.NewString(), uuid.NewString()
	h.devices.tokens[user] = []string{"phone"}
	_, first := h.signal(room, user)
	h.drain()
	require.True(t, h.marks.has(first, user))
	_, second := h.signal(room, user)
	_, third := h.signal(room, user)
	h.drain()
	require.False(t, h.marks.has(second, user), "no mark before the push")
	h.advance(10 * time.Second)
	require.True(t, h.marks.has(second, user))
	require.True(t, h.marks.has(third, user))
	require.Equal(t, h.now.Add(2*time.Hour), h.marks.marks[third+"/"+user], "marks outlive the signal")
}

// When one recipient fails temporarily, the signal is retried, and only
// recipients pushed successfully are marked. ADR 0003, acceptance test 4.
func TestDispatcherRetriesOnlyUnmarkedRecipients(t *testing.T) {
	h := newHarness(t, Config{})
	alice, bob, room := uuid.NewString(), uuid.NewString(), uuid.NewString()
	h.devices.tokens[alice] = []string{"alice-phone"}
	h.devices.tokens[bob] = []string{"bob-phone"}
	h.provider.answers["bob-phone"] = fmt.Errorf("%w: FCM answered 503", ErrTemporary)
	o, id := h.signal(room, alice, bob)
	h.drain()
	require.Equal(t, "retry", o.state(t))
	require.True(t, h.marks.has(id, alice))
	require.False(t, h.marks.has(id, bob))
	unmarked, err := h.marks.UnmarkedPushRecipients(context.Background(), id, []string{alice, bob})
	require.NoError(t, err)
	require.Equal(t, []string{bob}, unmarked, "the redelivery goes to bob only")
}

// A final provider answer for one device is not retried; the other devices
// get their push either way.
func TestDispatcherDeletesOnlyInvalidTokensAndRetriesOnlyTemporaryFailures(t *testing.T) {
	h := newHarness(t, Config{})
	user, room := uuid.NewString(), uuid.NewString()
	h.devices.tokens[user] = []string{"gone", "rejected", "fine"}
	h.provider.answers["gone"] = ErrTokenInvalid
	h.provider.answers["rejected"] = errors.New("FCM answered 400 INVALID_ARGUMENT")
	o, _ := h.signal(room, user)
	h.drain()
	require.Equal(t, []string{"gone"}, h.devices.deleted)
	require.Equal(t, Stats{Pending: 1, Sent: 1, Failed: 1, Invalid: 1}, h.d.Stats())
	require.Equal(t, "done", o.state(t), "UNREGISTERED and rejected requests are final")

	h.provider.answers["fine"] = fmt.Errorf("%w: network", ErrTemporary)
	h.advance(defaultWindow)
	h.advance(defaultWindow)
	o, _ = h.signal(room, user)
	h.drain()
	require.Equal(t, "retry", o.state(t))
	// The stub keeps the deleted token, so the second push goes to all three.
	require.Len(t, h.provider.messages(), 6, "the temporary failure did not stop the other devices")
}

// A login revoked while an earlier device of the user is being sent to gets
// no send started afterwards: the loaded tokens are not an authorization.
func TestDispatcherStartsNoSendForDevicesRevokedMeanwhile(t *testing.T) {
	h := newHarness(t, Config{})
	user, room := uuid.NewString(), uuid.NewString()
	h.devices.tokens[user] = []string{"first-device", "queued-device", "kept-device"}
	h.provider.onSend = func(m Message) {
		if m.Token == "first-device" {
			h.devices.revoke(user, "queued-device")
		}
	}
	h.signal(room, user)
	h.drain()
	require.Equal(t, []Message{{Token: "first-device", RoomID: room}, {Token: "kept-device", RoomID: room}}, h.provider.pushes())
	require.Equal(t, Stats{Pending: 1, Sent: 2, Skipped: 1}, h.d.Stats())
}

// When the device cannot be checked, nothing is sent to it, and the signal
// is retried.
func TestDispatcherSendsNothingWhenTheDeviceCannotBeChecked(t *testing.T) {
	h := newHarness(t, Config{})
	user, room := uuid.NewString(), uuid.NewString()
	h.devices.tokens[user] = []string{"phone", "tablet"}
	h.devices.targetErr = errors.New("database unavailable")
	o, id := h.signal(room, user)
	h.drain()
	require.Empty(t, h.provider.messages())
	require.Equal(t, Stats{Pending: 1, Failed: 1}, h.d.Stats(), "one failure stops the sends of the pair")
	require.Equal(t, "retry", o.state(t))
	require.False(t, h.marks.has(id, user))
}

func TestDispatcherSurvivesTokenStoreOutage(t *testing.T) {
	h := newHarness(t, Config{Window: time.Second})
	user, room := uuid.NewString(), uuid.NewString()
	h.devices.err = errors.New("database unavailable")
	o, _ := h.signal(room, user)
	h.drain()
	require.Empty(t, h.provider.messages())
	require.EqualValues(t, 1, h.d.Stats().Failed)
	require.Equal(t, "retry", o.state(t))

	h.devices.err = nil
	h.devices.tokens[user] = []string{"phone"}
	h.advance(time.Second)
	o, _ = h.signal(room, user)
	h.advance(time.Second)
	require.Equal(t, []Message{{Token: "phone", RoomID: room}}, h.provider.pushes(), "later signals are delivered")
	require.Equal(t, "done", o.state(t))
}

// A push that went out but could not be recorded is retried: the repeat is
// harmless, a lost push is not.
func TestDispatcherRetriesWhenTheMarkCannotBeWritten(t *testing.T) {
	h := newHarness(t, Config{})
	user, room := uuid.NewString(), uuid.NewString()
	h.devices.tokens[user] = []string{"phone"}
	h.marks.markErr = errors.New("database unavailable")
	o, _ := h.signal(room, user)
	h.drain()
	require.Len(t, h.provider.messages(), 1)
	require.Equal(t, "retry", o.state(t))
}

// The provider holds a push no longer than the rest of its signal's hour;
// a signal past its hour is finished without a push. ADR 0003, acceptance
// test 7 (the dispatcher side).
func TestDispatcherBoundsTheProviderTTLByTheSignalsHour(t *testing.T) {
	h := newHarness(t, Config{})
	alice, bob, room := uuid.NewString(), uuid.NewString(), uuid.NewString()
	h.devices.tokens[alice] = []string{"alice-phone"}
	h.devices.tokens[bob] = []string{"bob-phone"}
	h.signalPublished(h.now.Add(-50*time.Minute), room, alice)
	h.drain()
	require.Equal(t, 10*time.Minute, h.provider.messages()[0].TTL)

	o, id := h.signalPublished(h.now.Add(-time.Hour), room, bob)
	h.drain()
	require.Len(t, h.provider.messages(), 1, "an expired signal wakes no one")
	require.Equal(t, "done", o.state(t))
	require.True(t, h.marks.has(id, bob))
	require.EqualValues(t, 1, h.d.Stats().Expired)

	// Merged signals: the push lives as long as the newest of them.
	h.advance(defaultWindow)
	h.advance(defaultWindow)
	h.signalPublished(h.now.Add(-59*time.Minute), room, alice)
	h.drain()
	h.signalPublished(h.now.Add(-58*time.Minute), room, alice)
	h.signalPublished(h.now.Add(-59*time.Minute), room, alice)
	h.advance(defaultWindow)
	messages := h.provider.messages()
	require.Equal(t, 2*time.Minute-defaultWindow, messages[len(messages)-1].TTL)
}

// Free tells the consumer how many new pairs fit; recipients that do not
// fit are failed for a retry, never dropped silently.
func TestDispatcherBoundsPendingPairs(t *testing.T) {
	h := newHarness(t, Config{MaxPending: 3})
	room := uuid.NewString()
	users := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	require.Equal(t, 3, h.d.Free())
	o, _ := h.signal(room, users...)
	require.Zero(t, h.d.Free())
	require.Equal(t, Stats{Pending: 3, Dropped: 2}, h.d.Stats())
	require.Len(t, h.d.ready, 3, "the queue never holds more than the bound")
	h.drain()
	require.Equal(t, "retry", o.state(t))
}

func TestDispatcherFinishesADeliveryWithoutRecipientsAtOnce(t *testing.T) {
	h := newHarness(t, Config{})
	o, _ := h.signal(uuid.NewString())
	require.Equal(t, "done", o.state(t))
}

func TestDispatcherRunsConcurrentlyAndStops(t *testing.T) {
	d := NewDispatcher(&stubProvider{answers: map[string]error{}}, &stubDevices{tokens: map[string][]string{}}, newMemoryMarks(),
		slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Window: 20 * time.Millisecond, Workers: 4})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	var signals sync.WaitGroup
	for range 8 {
		signals.Go(func() {
			for range 100 {
				d.Submit(&Delivery{ID: uuid.NewString(), RoomID: uuid.NewString(), UserIDs: []string{uuid.NewString()},
					Expires: time.Now().Add(time.Hour), Done: func(bool) {}})
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
