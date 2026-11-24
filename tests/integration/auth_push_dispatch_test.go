package integration

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/push"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/events"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/pushbus"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/pushbus/pushbustest"
)

// recordingProvider stands in for FCM; tokens in invalid are rejected as
// unregistered.
type recordingProvider struct {
	mu      sync.Mutex
	sent    []push.Message
	invalid map[string]bool
}

func (p *recordingProvider) Send(_ context.Context, m push.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, m)
	if p.invalid[m.Token] {
		return push.ErrTokenInvalid
	}
	return nil
}

func (p *recordingProvider) messages() []push.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]push.Message(nil), p.sent...)
}

// TestPushDispatch_FromChatMessageToActiveDevices runs Chat's publisher,
// the deployment's NATS configuration, Auth's consumer and dispatcher, and
// the token and mark store. Auth is down while the message is sent and
// starts afterwards (ADR 0003, acceptance test 1).
func TestPushDispatch_FromChatMessageToActiveDevices(t *testing.T) {
	pool, sessions, sender := newSessionFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	users := repository.NewUserRepository(pool)
	recipient, loggedOut := newTestUser(), newTestUser()
	require.NoError(t, users.Create(ctx, recipient))
	require.NoError(t, users.Create(ctx, loggedOut))

	senderLogin := newLogin(t, sessions, sender.ID)
	require.NoError(t, sessions.RegisterPushToken(ctx, sender.ID, senderLogin.ID, "fcm", "sender-phone"))
	phone, stale := newLogin(t, sessions, recipient.ID), newLogin(t, sessions, recipient.ID)
	require.NoError(t, sessions.RegisterPushToken(ctx, recipient.ID, phone.ID, "fcm", "recipient-phone"))
	require.NoError(t, sessions.RegisterPushToken(ctx, recipient.ID, stale.ID, "fcm", "uninstalled-app"))
	gone := newLogin(t, sessions, loggedOut.ID)
	require.NoError(t, sessions.RegisterPushToken(ctx, loggedOut.ID, gone.ID, "fcm", "logged-out-phone"))
	require.NoError(t, sessions.RevokeFamily(ctx, loggedOut.ID, gone.ID))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broker := pushbustest.Start(t)
	connect := func(service string) *nats.Conn {
		conn, err := pushbus.Connect(broker.URL(), service, pushbustest.TLS(t, service, broker.CA.Issue(t, service)), logger)
		require.NoError(t, err)
		t.Cleanup(conn.Close)
		return conn
	}
	authJS, err := jetstream.New(connect("auth"))
	require.NoError(t, err)
	_, err = pushbus.Declare(ctx, authJS) // Auth ran once before
	require.NoError(t, err)
	signals, err := pushbus.NewPublisher(connect("chat"))
	require.NoError(t, err)

	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	publisher := events.NewPublisher(client, logger, events.WithPushSignals(signals))
	room := uuid.NewString()
	message := &domain.Message{ID: uuid.NewString(), RoomID: room, SenderID: sender.ID, Text: "synthetic text", CreatedAt: time.Now()}
	publisher.Notify(ctx, domain.Event{Kind: domain.MessageCreated, RoomID: room, Message: message,
		Recipients: []string{sender.ID, recipient.ID, loggedOut.ID}})
	closeCtx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	require.NoError(t, publisher.Close(closeCtx))
	require.EqualValues(t, 1, publisher.Stats().SignalsStored)

	provider := &recordingProvider{invalid: map[string]bool{"uninstalled-app": true}}
	dispatcher := push.NewDispatcher(provider, sessions, sessions, logger, push.Config{Window: time.Second})
	consumer := push.NewConsumer(push.NewDeclaringSource(authJS), dispatcher, sessions, logger, push.ConsumerConfig{})
	go dispatcher.Run(ctx)
	go consumer.Run(ctx)

	require.Eventually(t, func() bool { return consumer.Stats().Acked == 1 }, 10*time.Second, 10*time.Millisecond)
	require.ElementsMatch(t, []push.Message{{Token: "recipient-phone", RoomID: room}, {Token: "uninstalled-app", RoomID: room}},
		withoutTTL(provider.messages()), "neither the sender nor a revoked login is woken")
	require.Equal(t, map[string]string{phone.ID: "recipient-phone"}, pushTokens(t, pool, recipient.ID),
		"the token the provider rejected is deleted")
	unmarked, err := sessions.UnmarkedPushRecipients(ctx, pushbus.SignalID(message.ID, 0), []string{recipient.ID, loggedOut.ID})
	require.NoError(t, err)
	require.Empty(t, unmarked, "every recipient is marked before the signal is acknowledged")
}

func withoutTTL(messages []push.Message) []push.Message {
	for i := range messages {
		messages[i].TTL = 0
	}
	return messages
}
