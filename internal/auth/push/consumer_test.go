package push

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/eventbus"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/pushbus"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/pushbus/pushbustest"
)

// rig is the deployment's broker with Auth's consumer, a dispatcher over
// stubs and a Chat publisher.
type rig struct {
	t         *testing.T
	srv       *pushbustest.Server
	consumer  jetstream.Consumer
	source    Source // the consumer unless set
	chat      *pushbus.Publisher
	chatConn  *nats.Conn
	provider  *stubProvider
	devices   *stubDevices
	marks     *memoryMarks
	d         *Dispatcher
	c         *Consumer
	cancel    context.CancelFunc
	stopped   chan struct{}
	dispCfg   Config
	consCfg   ConsumerConfig
	clockSkew time.Duration
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// fastRetries shortens ADR 0003's delays, in their order.
var fastRetries = []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 150 * time.Millisecond, 200 * time.Millisecond}

func newRig(t *testing.T, dispCfg Config, consCfg ConsumerConfig) *rig {
	t.Helper()
	srv := pushbustest.Start(t)
	conn := func(service string) *nats.Conn {
		c, err := pushbus.Connect(srv.URL(), service, pushbustest.TLS(t, service, srv.CA.Issue(t, service)), quiet)
		require.NoError(t, err)
		t.Cleanup(c.Close)
		return c
	}
	js, err := jetstream.New(conn("auth"))
	require.NoError(t, err)
	consumer, err := pushbus.Declare(context.Background(), js)
	require.NoError(t, err)
	chatConn := conn("chat")
	chat, err := pushbus.NewPublisher(chatConn)
	require.NoError(t, err)
	if len(consCfg.RetryDelays) == 0 {
		consCfg.RetryDelays = fastRetries
	}
	r := &rig{t: t, srv: srv, consumer: consumer, chat: chat, chatConn: chatConn, provider: &stubProvider{answers: map[string]error{}},
		devices: &stubDevices{tokens: map[string][]string{}}, marks: newMemoryMarks(), dispCfg: dispCfg, consCfg: consCfg}
	t.Cleanup(r.stop)
	return r
}

// start runs a fresh dispatcher and consumer, as a starting Auth does.
func (r *rig) start() {
	r.d = NewDispatcher(r.provider, r.devices, r.marks, quiet, r.dispCfg)
	r.c = NewConsumer(r.pullFrom(), r.d, r.marks, quiet, r.consCfg)
	skewed := func() time.Time { return time.Now().Add(r.clockSkew) }
	r.d.now, r.c.now = skewed, skewed
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel, r.stopped = cancel, make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		wg.Go(func() { r.d.Run(ctx) })
		r.c.Run(ctx)
		wg.Wait()
		close(r.stopped)
	}()
}

func (r *rig) pullFrom() Source {
	if r.source != nil {
		return r.source
	}
	return r.consumer
}

func (r *rig) stop() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	select {
	case <-r.stopped:
	case <-time.After(20 * time.Second):
		r.t.Error("consumer did not stop")
	}
	r.cancel = nil
}

// publish stores a signal from Chat and returns its id.
func (r *rig) publish(room string, users ...string) string {
	r.t.Helper()
	id := pushbus.SignalID(uuid.NewString(), 0)
	r.publishID(id, room, users...)
	return id
}

// publishID stores a signal from Chat under a chosen id.
func (r *rig) publishID(id, room string, users ...string) {
	r.t.Helper()
	payloads, err := eventbus.EncodePushSignals(room, users)
	require.NoError(r.t, err)
	lost, err := r.chat.Publish(context.Background(), []pushbus.Signal{{ID: id, Payload: payloads[0]}})
	require.NoError(r.t, err)
	require.Zero(r.t, lost)
}

func (r *rig) info() *jetstream.ConsumerInfo {
	r.t.Helper()
	info, err := r.consumer.Info(context.Background())
	require.NoError(r.t, err)
	return info
}

// settled waits until the stream holds nothing: every signal acknowledged
// or terminated.
func (r *rig) settled() {
	r.t.Helper()
	require.Eventually(r.t, func() bool {
		info := r.info()
		return info.NumPending == 0 && info.NumAckPending == 0
	}, 10*time.Second, 20*time.Millisecond)
}

func (r *rig) tokensOf(user string) []string { return r.provider.tokensTo(user, r.devices) }

// tokensTo returns the tokens of user that were sent a push, in order.
func (p *stubProvider) tokensTo(user string, devices *stubDevices) []string {
	devices.mu.Lock()
	owned := map[string]bool{}
	for _, token := range devices.tokens[user] {
		owned[token] = true
	}
	devices.mu.Unlock()
	var tokens []string
	for _, m := range p.messages() {
		if owned[m.Token] {
			tokens = append(tokens, m.Token)
		}
	}
	return tokens
}

// Signals stored while Auth is down are delivered when it starts: one push
// per recipient. ADR 0003, acceptance test 1.
func TestConsumerDeliversSignalsStoredWhileStopped(t *testing.T) {
	r := newRig(t, Config{}, ConsumerConfig{})
	alice, bob, room := uuid.NewString(), uuid.NewString(), uuid.NewString()
	r.devices.tokens[alice] = []string{"alice-phone"}
	r.devices.tokens[bob] = []string{"bob-phone"}
	r.publish(room, alice, bob)
	require.EqualValues(t, 1, r.info().NumPending)

	r.start()
	r.settled()
	require.ElementsMatch(t, []Message{{Token: "alice-phone", RoomID: room}, {Token: "bob-phone", RoomID: room}}, r.provider.pushes())
	require.EqualValues(t, 1, r.c.Stats().Acked)
}

// A redelivered signal skips recipients that already have a mark, so a
// crash after the mark and before the ACK repeats no push. ADR 0003,
// acceptance test 2.
func TestConsumerSkipsMarkedRecipients(t *testing.T) {
	r := newRig(t, Config{}, ConsumerConfig{})
	alice, bob, room := uuid.NewString(), uuid.NewString(), uuid.NewString()
	r.devices.tokens[alice] = []string{"alice-phone"}
	r.devices.tokens[bob] = []string{"bob-phone"}
	id := r.publish(room, alice, bob)
	require.NoError(t, r.marks.MarkPushRecipient(context.Background(), []string{id}, alice, time.Now().Add(time.Hour)))

	r.start()
	r.settled()
	require.Equal(t, []Message{{Token: "bob-phone", RoomID: room}}, r.provider.pushes())

	// Fully marked: acknowledged without a push. The consumer is already
	// running, so the mark must exist before the signal is published;
	// marking afterwards races the delivery.
	id = pushbus.SignalID(uuid.NewString(), 0)
	require.NoError(t, r.marks.MarkPushRecipient(context.Background(), []string{id}, alice, time.Now().Add(time.Hour)))
	r.publishID(id, room, alice)
	r.settled()
	require.Len(t, r.provider.messages(), 1)
}

// failingMarks fails the first n mark writes, as a crash between FCM
// accepting the push and the mark being written.
type failingMarks struct {
	*memoryMarks
	failures atomic.Int32
}

func (m *failingMarks) MarkPushRecipient(ctx context.Context, ids []string, user string, expires time.Time) error {
	if m.failures.Add(-1) >= 0 {
		return fmt.Errorf("connection reset")
	}
	return m.memoryMarks.MarkPushRecipient(ctx, ids, user, expires)
}

// A push accepted by FCM whose mark was not written is repeated exactly
// once for that recipient; recipients with marks get nothing again. ADR
// 0003, acceptance test 3.
func TestConsumerRepeatsAPushWhoseMarkWasLostOnce(t *testing.T) {
	// One worker sends the pairs in the signal's order, so the first mark
	// write, alice's, is the one that fails.
	r := newRig(t, Config{Window: 10 * time.Millisecond, Workers: 1}, ConsumerConfig{})
	alice, bob, room := uuid.NewString(), uuid.NewString(), uuid.NewString()
	r.devices.tokens[alice] = []string{"alice-phone"}
	r.devices.tokens[bob] = []string{"bob-phone"}
	marks := &failingMarks{memoryMarks: r.marks}
	marks.failures.Store(1)
	r.publish(room, alice, bob)
	r.startWithMarks(marks)
	r.settled()
	require.Equal(t, []string{"alice-phone", "alice-phone"}, r.tokensOf(alice), "exactly one repeat")
	require.Equal(t, []string{"bob-phone"}, r.tokensOf(bob), "a recipient with a mark gets nothing again")
	require.EqualValues(t, 1, r.c.Stats().Retried)
}

func (r *rig) startWithMarks(marks Marks) {
	r.d = NewDispatcher(r.provider, r.devices, marks, quiet, r.dispCfg)
	r.c = NewConsumer(r.pullFrom(), r.d, marks, quiet, r.consCfg)
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel, r.stopped = cancel, make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		wg.Go(func() { r.d.Run(ctx) })
		r.c.Run(ctx)
		wg.Wait()
		close(r.stopped)
	}()
}

// Partial success: alice's push went out, bob's failed temporarily; the
// redelivery goes to bob only. ADR 0003, acceptance test 4.
func TestConsumerRedeliversOnlyToFailedRecipients(t *testing.T) {
	r := newRig(t, Config{Window: 10 * time.Millisecond}, ConsumerConfig{})
	alice, bob, room := uuid.NewString(), uuid.NewString(), uuid.NewString()
	r.devices.tokens[alice] = []string{"alice-phone"}
	r.devices.tokens[bob] = []string{"bob-phone"}
	var bobFailures atomic.Int32
	bobFailures.Store(1)
	r.provider.onSend = func(m Message) {
		r.provider.mu.Lock()
		defer r.provider.mu.Unlock()
		if m.Token == "bob-phone" && bobFailures.Add(-1) >= 0 {
			r.provider.answers["bob-phone"] = fmt.Errorf("%w: 503", ErrTemporary)
		} else {
			delete(r.provider.answers, "bob-phone")
		}
	}
	r.publish(room, alice, bob)
	r.start()
	r.settled()
	require.Equal(t, []string{"alice-phone"}, r.tokensOf(alice))
	require.Equal(t, []string{"bob-phone", "bob-phone"}, r.tokensOf(bob))
	require.EqualValues(t, 1, r.c.Stats().Retried)
	require.EqualValues(t, 1, r.c.Stats().Acked)
}

// A signal waiting in the collapsing window longer than AckWait is kept
// alive by InProgress and not redelivered. ADR 0003, acceptance test 5.
func TestConsumerKeepsWaitingSignalsInProgress(t *testing.T) {
	r := newRig(t, Config{Window: 2500 * time.Millisecond}, ConsumerConfig{Progress: 300 * time.Millisecond})
	cfg := pushbus.ConsumerConfig()
	cfg.AckWait = time.Second
	js, err := jetstream.New(r.authConn())
	require.NoError(t, err)
	r.consumer, err = js.CreateOrUpdateConsumer(context.Background(), pushbus.StreamName, cfg)
	require.NoError(t, err)

	user, room := uuid.NewString(), uuid.NewString()
	r.devices.tokens[user] = []string{"phone"}
	r.start()
	r.publish(room, user)
	require.Eventually(t, func() bool { return len(r.provider.messages()) == 1 }, 5*time.Second, 10*time.Millisecond)
	r.publish(room, user) // waits ~2.5 s in the window, AckWait is 1 s
	r.settled()
	require.Len(t, r.provider.messages(), 2)
	require.EqualValues(t, 2, r.c.Stats().Received, "no delivery was considered lost and redelivered")
}

func (r *rig) authConn() *nats.Conn {
	r.t.Helper()
	c, err := pushbus.Connect(r.srv.URL(), "auth", pushbustest.TLS(r.t, "auth", r.srv.CA.Issue(r.t, "auth")), quiet)
	require.NoError(r.t, err)
	r.t.Cleanup(c.Close)
	return c
}

// With 20 signals in work, the 21st is not delivered until one is
// acknowledged. ADR 0003, acceptance test 6.
func TestConsumerHoldsAtMostMaxAckPendingSignals(t *testing.T) {
	r := newRig(t, Config{Workers: pushbus.MaxAckPending + 4}, ConsumerConfig{Progress: 200 * time.Millisecond})
	release := make(chan struct{})
	r.provider.onSend = func(Message) { <-release }
	room := uuid.NewString()
	for i := range pushbus.MaxAckPending + 1 {
		user := uuid.NewString()
		r.devices.tokens[user] = []string{fmt.Sprintf("phone-%d", i)}
		r.publish(room, user)
	}
	r.start()
	require.Eventually(t, func() bool { return r.c.Stats().InWork == pushbus.MaxAckPending }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	info := r.info()
	require.EqualValues(t, pushbus.MaxAckPending, info.NumAckPending)
	require.EqualValues(t, 1, info.NumPending, "the 21st waits in the stream")
	require.Equal(t, pushbus.MaxAckPending, r.c.Stats().InWork)
	close(release)
	r.settled()
	require.Len(t, r.provider.messages(), pushbus.MaxAckPending+1)
}

// A signal that lay in the stream for 50 minutes is pushed with a TTL of
// about 10 minutes; one older than an hour is acknowledged without a push.
// ADR 0003, acceptance test 7.
func TestConsumerBoundsTTLByTheTimeInTheStream(t *testing.T) {
	r := newRig(t, Config{}, ConsumerConfig{})
	user, room := uuid.NewString(), uuid.NewString()
	r.devices.tokens[user] = []string{"phone"}
	r.publish(room, user)
	r.clockSkew = 50 * time.Minute
	r.start()
	r.settled()
	require.Len(t, r.provider.messages(), 1)
	require.InDelta(t, (10 * time.Minute).Seconds(), r.provider.messages()[0].TTL.Seconds(), 5)
	r.stop()

	r.publish(uuid.NewString(), user)
	r.clockSkew = time.Hour + time.Second
	r.start()
	r.settled()
	require.Len(t, r.provider.messages(), 1, "an expired signal wakes no one")
	require.EqualValues(t, 1, r.c.Stats().Expired)
	require.EqualValues(t, 1, r.c.Stats().Acked)
}

// Malformed signals and signals without a valid id are terminated at once
// and never redelivered. ADR 0003, acceptance test 9.
func TestConsumerTerminatesMalformedSignals(t *testing.T) {
	r := newRig(t, Config{}, ConsumerConfig{})
	js, err := jetstream.New(r.chatConn)
	require.NoError(t, err)
	valid, err := eventbus.EncodePushSignals(uuid.NewString(), []string{uuid.NewString()})
	require.NoError(t, err)
	for _, msg := range []*nats.Msg{
		{Subject: pushbus.Subject, Data: []byte(`{"room_id":"x"}`), Header: nats.Header{jetstream.MsgIDHeader: {pushbus.SignalID("a", 0)}}},
		{Subject: pushbus.Subject, Data: valid[0], Header: nats.Header{jetstream.MsgIDHeader: {"not-a-hash"}}},
		{Subject: pushbus.Subject, Data: valid[0]},
	} {
		_, err := js.PublishMsg(context.Background(), msg)
		require.NoError(t, err)
	}
	r.start()
	r.settled()
	require.Empty(t, r.provider.messages())
	require.EqualValues(t, 3, r.c.Stats().Terminated)
	require.EqualValues(t, 3, r.d.Stats().Malformed)
	require.EqualValues(t, 3, r.c.Stats().Received, "terminated signals are not redelivered")
}

// A temporary FCM failure is retried after growing delays and succeeds
// when FCM recovers; after the fifth failed delivery the signal is
// terminated. ADR 0003, acceptance test 10.
func TestConsumerRetriesTemporaryFailuresWithGrowingDelays(t *testing.T) {
	r := newRig(t, Config{Window: time.Millisecond}, ConsumerConfig{})
	user, room := uuid.NewString(), uuid.NewString()
	r.devices.tokens[user] = []string{"phone"}
	var sends []time.Time
	var mu sync.Mutex
	var failing atomic.Int32
	failing.Store(2)
	r.provider.onSend = func(Message) {
		mu.Lock()
		sends = append(sends, time.Now())
		mu.Unlock()
		r.provider.mu.Lock()
		defer r.provider.mu.Unlock()
		if failing.Add(-1) >= 0 {
			r.provider.answers["phone"] = fmt.Errorf("%w: 503", ErrTemporary)
		} else {
			delete(r.provider.answers, "phone")
		}
	}
	r.publish(room, user)
	r.start()
	r.settled()
	require.Len(t, r.provider.messages(), 3, "sent after recovery")
	require.EqualValues(t, 1, r.c.Stats().Acked)
	mu.Lock()
	require.GreaterOrEqual(t, sends[1].Sub(sends[0]), fastRetries[0])
	require.GreaterOrEqual(t, sends[2].Sub(sends[1]), fastRetries[1])
	mu.Unlock()

	failing.Store(1000)
	r.publish(uuid.NewString(), user)
	r.settled()
	require.Len(t, r.provider.messages(), 3+pushbus.MaxDeliver, "five deliveries, then no more")
	require.EqualValues(t, 1, r.c.Stats().Exhausted)
	require.EqualValues(t, 1, r.c.Stats().Terminated)

	// A final answer is acknowledged at once.
	r.provider.onSend = nil
	r.provider.answers["phone"] = fmt.Errorf("FCM answered 400 INVALID_ARGUMENT")
	r.publish(uuid.NewString(), user)
	r.settled()
	require.Len(t, r.provider.messages(), 4+pushbus.MaxDeliver)
	require.EqualValues(t, 2, r.c.Stats().Acked)
}

// Stopping Auth gives the signals in work back at once: the next run gets
// them without waiting for AckWait.
func TestConsumerGivesBackSignalsInWorkOnStop(t *testing.T) {
	r := newRig(t, Config{Window: time.Hour}, ConsumerConfig{})
	user, room := uuid.NewString(), uuid.NewString()
	r.devices.tokens[user] = []string{"phone"}
	r.start()
	r.publish(room, user)
	require.Eventually(t, func() bool { return len(r.provider.messages()) == 1 }, 5*time.Second, 10*time.Millisecond)
	waiting := r.publish(room, user) // held by the hour-long window
	require.Eventually(t, func() bool { return r.c.Stats().InWork == 1 }, 5*time.Second, 10*time.Millisecond)
	r.stop()

	started := time.Now()
	r.start()
	r.settled()
	require.Less(t, time.Since(started), pushbus.AckWait/2)
	require.Len(t, r.provider.messages(), 2)
	require.True(t, r.marks.has(waiting, user))
}

// A broker that lost its store gets the stream and consumer declared again,
// and delivery resumes without restarting Auth.
func TestDeclaringSourceRedeclaresALostStream(t *testing.T) {
	r := newRig(t, Config{Window: time.Millisecond}, ConsumerConfig{})
	user := uuid.NewString()
	r.devices.tokens[user] = []string{"phone"}
	js, err := jetstream.New(r.authConn())
	require.NoError(t, err)
	r.source = NewDeclaringSource(js)
	r.start()
	r.publish(uuid.NewString(), user)
	require.Eventually(t, func() bool { return len(r.provider.messages()) == 1 }, 5*time.Second, 10*time.Millisecond)

	r.srv.RestartEmpty()
	require.Eventually(t, func() bool {
		payloads, err := eventbus.EncodePushSignals(uuid.NewString(), []string{user})
		require.NoError(t, err)
		lost, _ := r.chat.Publish(context.Background(), []pushbus.Signal{{ID: pushbus.SignalID(uuid.NewString(), 0), Payload: payloads[0]}})
		return lost == 0
	}, 20*time.Second, 200*time.Millisecond, "the stream is declared again")
	require.Eventually(t, func() bool { return len(r.provider.messages()) >= 2 }, 10*time.Second, 10*time.Millisecond)
}
