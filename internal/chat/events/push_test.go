package events_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/events"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/eventbus"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/pushbus"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/pushbus/pushbustest"
)

var discard = slog.New(slog.NewJSONHandler(io.Discard, nil))

// recordingSignals stores what it is given, or fails with err.
type recordingSignals struct {
	mu      sync.Mutex
	signals []pushbus.Signal
	err     error
}

func (r *recordingSignals) Publish(_ context.Context, signals []pushbus.Signal) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return len(signals), r.err
	}
	r.signals = append(r.signals, signals...)
	return 0, nil
}

func (r *recordingSignals) all() []pushbus.Signal {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]pushbus.Signal(nil), r.signals...)
}

func newMessage(room, sender string) *domain.Message {
	return &domain.Message{ID: uuid.NewString(), RoomID: room, SenderID: sender, Text: "secret text", CreatedAt: time.Now()}
}

func TestPublisher_SignalsPushForNewMessagesWithoutContent(t *testing.T) {
	sender, other, third := uuid.NewString(), uuid.NewString(), uuid.NewString()
	room := uuid.NewString()
	message := newMessage(room, sender)
	signals := &recordingSignals{}
	publisher := events.NewPublisher(newClient(t), discard, events.WithPushSignals(signals))
	publisher.Notify(context.Background(),
		domain.Event{Kind: domain.MessageCreated, RoomID: room, Message: message, Recipients: []string{sender, other, third}},
		// Membership changes and a message only its sender receives wake no one.
		domain.Event{Kind: domain.MemberAdded, RoomID: room, UserID: other, Recipients: []string{sender, other}},
		domain.Event{Kind: domain.MessageCreated, RoomID: room, Message: newMessage(room, sender), Recipients: []string{sender}},
	)
	closePublisher(t, publisher)

	stored := signals.all()
	require.Len(t, stored, 1, "only the message to others signals a push")
	require.Equal(t, pushbus.SignalID(message.ID, 0), stored[0].ID)
	require.NotContains(t, string(stored[0].Payload), "secret text")
	require.NotContains(t, string(stored[0].Payload), sender, "the sender is neither woken nor named")
	require.NotContains(t, string(stored[0].Payload), message.ID)
	signal, err := eventbus.DecodePushSignal(stored[0].Payload)
	require.NoError(t, err)
	require.Equal(t, eventbus.PushSignal{RoomID: room, UserIDs: []string{other, third}}, signal)
	require.Equal(t, uint64(1), publisher.Stats().SignalsStored)
}

// A large room is split into signals of at most 500 recipients, each with
// an id of its own part.
func TestPublisher_SplitsSignalsOfLargeRooms(t *testing.T) {
	room, sender := uuid.NewString(), uuid.NewString()
	recipients := []string{sender}
	for range 2*eventbus.MaxPushSignalRecipients + 1 {
		recipients = append(recipients, uuid.NewString())
	}
	message := newMessage(room, sender)
	signals := &recordingSignals{}
	publisher := events.NewPublisher(newClient(t), discard, events.WithPushSignals(signals))
	publisher.Notify(context.Background(), domain.Event{Kind: domain.MessageCreated, RoomID: room, Message: message, Recipients: recipients})
	closePublisher(t, publisher)

	stored := signals.all()
	require.Len(t, stored, 3)
	var woken []string
	for part, s := range stored {
		require.Equal(t, pushbus.SignalID(message.ID, part), s.ID)
		signal, err := eventbus.DecodePushSignal(s.Payload)
		require.NoError(t, err)
		woken = append(woken, signal.UserIDs...)
	}
	require.Equal(t, recipients[1:], woken)
}

// Without a signal publisher nothing is signalled: push is off.
func TestPublisher_WithoutPushSignalsMakesNone(t *testing.T) {
	publisher := events.NewPublisher(newClient(t), discard)
	room := uuid.NewString()
	publisher.Notify(context.Background(), domain.Event{Kind: domain.MessageCreated, RoomID: room,
		Message: newMessage(room, uuid.NewString()), Recipients: []string{uuid.NewString()}})
	closePublisher(t, publisher)
	require.Zero(t, publisher.Stats().SignalsLost)
	require.Zero(t, publisher.Stats().SignalsStored)
}

// A broker failure loses the wake-up only: the event still reaches the
// recipient's channel, and the loss is counted.
func TestPublisher_SignalFailureDoesNotStopEvents(t *testing.T) {
	client := newClient(t)
	recipient := uuid.NewString()
	channel := subscribe(t, client, recipient)
	room := uuid.NewString()
	publisher := events.NewPublisher(client, discard, events.WithPushSignals(&recordingSignals{err: errors.New("broker down")}))
	publisher.Notify(context.Background(), domain.Event{Kind: domain.MessageCreated, RoomID: room,
		Message: newMessage(room, uuid.NewString()), Recipients: []string{recipient}})
	closePublisher(t, publisher)

	_, err := channel.ReceiveMessage(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), publisher.Stats().SignalsLost)
	require.Zero(t, publisher.Stats().SignalsStored)
}

// brokerFixture is the deployment's broker with the stream declared by Auth
// and a Chat publisher.
type brokerFixture struct {
	srv  *pushbustest.Server
	auth jetstream.JetStream
	chat *pushbus.Publisher
}

func newBroker(t *testing.T) brokerFixture {
	t.Helper()
	srv := pushbustest.Start(t)
	conn := func(service string) *nats.Conn {
		c, err := pushbus.Connect(srv.URL(), service, pushbustest.TLS(t, service, srv.CA.Issue(t, service)), discard)
		require.NoError(t, err)
		t.Cleanup(c.Close)
		return c
	}
	auth, err := jetstream.New(conn("auth"))
	require.NoError(t, err)
	_, err = pushbus.Declare(context.Background(), auth)
	require.NoError(t, err)
	chat, err := pushbus.NewPublisher(conn("chat"))
	require.NoError(t, err)
	return brokerFixture{srv: srv, auth: auth, chat: chat}
}

func (b brokerFixture) stored(t *testing.T) uint64 {
	t.Helper()
	stream, err := b.auth.Stream(context.Background(), pushbus.StreamName)
	require.NoError(t, err)
	return stream.CachedInfo().State.Msgs
}

func messageEvent(room string, recipients ...string) domain.Event {
	return domain.Event{Kind: domain.MessageCreated, RoomID: room, Message: newMessage(room, uuid.NewString()), Recipients: recipients}
}

// The same signal published twice within the duplicate window is stored
// once. ADR 0003, acceptance test 8.
func TestPublisher_RepeatedSignalIsStoredOnce(t *testing.T) {
	broker := newBroker(t)
	room := uuid.NewString()
	event := messageEvent(room, uuid.NewString())
	publisher := events.NewPublisher(newClient(t), discard, events.WithPushSignals(broker.chat))
	publisher.Notify(context.Background(), event)
	publisher.Notify(context.Background(), event)
	closePublisher(t, publisher)
	require.Equal(t, uint64(1), broker.stored(t))
	require.Zero(t, publisher.Stats().SignalsLost)
}

// With the broker down, sending is not held up and the event is delivered;
// the signal is counted lost. After the broker returns, new signals are
// stored again. ADR 0003, acceptance test 11.
func TestPublisher_BrokerOutageLosesOnlySignals(t *testing.T) {
	broker := newBroker(t)
	client := newClient(t)
	recipient := uuid.NewString()
	channel := subscribe(t, client, recipient)
	publisher := events.NewPublisher(client, discard, events.WithPushSignals(broker.chat))
	t.Cleanup(func() { closePublisher(t, publisher) })

	broker.srv.Shutdown()
	started := time.Now()
	publisher.Notify(context.Background(), messageEvent(uuid.NewString(), recipient))
	_, err := channel.ReceiveMessage(context.Background())
	require.NoError(t, err)
	require.Eventually(t, func() bool { return publisher.Stats().SignalsLost == 1 }, 3*time.Second, 10*time.Millisecond)
	require.Less(t, time.Since(started), 2*time.Second, "an unreachable broker fails fast")

	broker.srv.Restart()
	require.Eventually(t, func() bool {
		publisher.Notify(context.Background(), messageEvent(uuid.NewString(), recipient))
		return publisher.Stats().SignalsStored > 0
	}, 10*time.Second, 200*time.Millisecond)
}

// A full stream refuses new signals rather than evicting unread ones; Chat
// counts the loss and goes on. ADR 0003, acceptance test 13.
func TestPublisher_FullStreamRefusesNewSignals(t *testing.T) {
	broker := newBroker(t)
	cfg := pushbus.StreamConfig()
	cfg.MaxMsgs = 1
	_, err := broker.auth.UpdateStream(context.Background(), cfg)
	require.NoError(t, err)

	client := newClient(t)
	recipient := uuid.NewString()
	channel := subscribe(t, client, recipient)
	publisher := events.NewPublisher(client, discard, events.WithPushSignals(broker.chat))
	first := messageEvent(uuid.NewString(), recipient)
	publisher.Notify(context.Background(), first, messageEvent(uuid.NewString(), recipient))
	closePublisher(t, publisher)

	for range 2 {
		_, err := channel.ReceiveMessage(context.Background())
		require.NoError(t, err)
	}
	require.Equal(t, uint64(1), publisher.Stats().SignalsStored)
	require.Equal(t, uint64(1), publisher.Stats().SignalsLost)
	require.Equal(t, uint64(1), broker.stored(t))
	consumer, err := broker.auth.Consumer(context.Background(), pushbus.StreamName, pushbus.ConsumerName)
	require.NoError(t, err)
	kept, err := consumer.Next(jetstream.FetchMaxWait(2 * time.Second))
	require.NoError(t, err)
	require.Equal(t, pushbus.SignalID(first.Message.ID, 0), kept.Headers().Get(jetstream.MsgIDHeader), "the unread signal is kept")
}
