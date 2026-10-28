package events_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/events"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/eventbus"
)

func TestPublisher_SignalsPushForNewMessagesWithoutContent(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	signals := client.Subscribe(ctx, eventbus.PushSignalChannel)
	t.Cleanup(func() { _ = signals.Close() })
	_, err := signals.Receive(ctx)
	require.NoError(t, err)

	sender, other, third := uuid.NewString(), uuid.NewString(), uuid.NewString()
	room := uuid.NewString()
	message := &domain.Message{ID: uuid.NewString(), RoomID: room, SenderID: sender, Text: "secret text", CreatedAt: time.Now()}
	publisher := events.NewPublisher(client, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	publisher.Notify(ctx,
		domain.Event{Kind: domain.MessageCreated, RoomID: room, Message: message, Recipients: []string{sender, other, third}},
		// Membership changes and a message only its sender receives wake no one.
		domain.Event{Kind: domain.MemberAdded, RoomID: room, UserID: other, Recipients: []string{sender, other}},
		domain.Event{Kind: domain.MessageCreated, RoomID: room, Message: message, Recipients: []string{sender}},
	)
	closePublisher(t, publisher)

	received, err := signals.ReceiveMessage(ctx)
	require.NoError(t, err)
	require.NotContains(t, received.Payload, "secret text")
	require.NotContains(t, received.Payload, sender, "the sender is neither woken nor named")
	signal, err := eventbus.DecodePushSignal([]byte(received.Payload))
	require.NoError(t, err)
	require.Equal(t, eventbus.PushSignal{RoomID: room, UserIDs: []string{other, third}}, signal)

	waitCtx, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stop()
	_, err = signals.ReceiveMessage(waitCtx)
	require.Error(t, err, "only the message to others signals a push")
}
