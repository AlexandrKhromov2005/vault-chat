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
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/push"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/events"
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
// Redis Pub/Sub, Auth's subscriber and dispatcher, and the token store.
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

	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	provider := &recordingProvider{invalid: map[string]bool{"uninstalled-app": true}}
	dispatcher := push.NewDispatcher(provider, sessions, logger, push.Config{Window: time.Second})
	go dispatcher.Run(ctx)
	subscribed := make(chan struct{})
	go func() {
		// Subscribe returns only when ctx ends.
		close(subscribed)
		_ = push.Subscribe(ctx, client, dispatcher, logger)
	}()
	<-subscribed
	require.Eventually(t, func() bool {
		n, err := client.PubSubNumSub(ctx, "push:signal:v1").Result()
		return err == nil && n["push:signal:v1"] == 1
	}, 5*time.Second, 10*time.Millisecond)

	publisher := events.NewPublisher(client, logger)
	room := uuid.NewString()
	message := &domain.Message{ID: uuid.NewString(), RoomID: room, SenderID: sender.ID, Text: "synthetic text", CreatedAt: time.Now()}
	publisher.Notify(ctx, domain.Event{Kind: domain.MessageCreated, RoomID: room, Message: message,
		Recipients: []string{sender.ID, recipient.ID, loggedOut.ID}})
	closeCtx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	require.NoError(t, publisher.Close(closeCtx))

	require.Eventually(t, func() bool { return len(provider.messages()) == 2 }, 5*time.Second, 10*time.Millisecond)
	// Sends for other users run on other workers: give a wrong push to the
	// sender or the logged-out user time to arrive before checking.
	time.Sleep(500 * time.Millisecond)
	require.ElementsMatch(t, []push.Message{{Token: "recipient-phone", RoomID: room}, {Token: "uninstalled-app", RoomID: room}},
		provider.messages(), "neither the sender nor a revoked login is woken")
	require.Eventually(t, func() bool {
		return len(pushTokens(t, pool, recipient.ID)) == 1
	}, 5*time.Second, 10*time.Millisecond, "the token the provider rejected is deleted")
	require.Equal(t, map[string]string{phone.ID: "recipient-phone"}, pushTokens(t, pool, recipient.ID))
}
