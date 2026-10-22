package events_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/events"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/eventbus"
)

func TestEncode_EveryKind(t *testing.T) {
	room, user := uuid.NewString(), uuid.NewString()
	created := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	message := &domain.Message{ID: uuid.NewString(), RoomID: room, SenderID: user, ClientID: uuid.NewString(), Text: "hi", Sequence: 7, CreatedAt: created}
	change := &chatv1.MemberChange{RoomId: room, UserId: user}
	for _, tc := range []struct {
		event domain.Event
		want  *chatv1.Event
	}{
		{domain.Event{Kind: domain.MessageCreated, RoomID: room, Message: message}, &chatv1.Event{Kind: &chatv1.Event_MessageCreated{MessageCreated: &chatv1.Message{
			Id: message.ID, RoomId: room, SenderId: user, ClientMessageId: message.ClientID, Text: "hi", Sequence: 7, CreatedAt: timestamppb.New(created),
		}}}},
		{domain.Event{Kind: domain.MemberAdded, RoomID: room, UserID: user}, &chatv1.Event{Kind: &chatv1.Event_MemberAdded{MemberAdded: change}}},
		{domain.Event{Kind: domain.MemberRemoved, RoomID: room, UserID: user}, &chatv1.Event{Kind: &chatv1.Event_MemberRemoved{MemberRemoved: change}}},
		{domain.Event{Kind: domain.MemberLeft, RoomID: room, UserID: user}, &chatv1.Event{Kind: &chatv1.Event_MemberLeft{MemberLeft: change}}},
	} {
		t.Run(string(tc.event.Kind), func(t *testing.T) {
			payload, err := events.Encode(tc.event)
			require.NoError(t, err)
			got := &chatv1.Event{}
			require.NoError(t, proto.Unmarshal(payload, got))
			require.True(t, proto.Equal(tc.want, got), "got %v", got)
		})
	}
	for _, event := range []domain.Event{{Kind: "room.renamed", RoomID: room}, {Kind: domain.MessageCreated, RoomID: room}} {
		_, err := events.Encode(event)
		require.Error(t, err)
	}
}

// syncBuffer collects logs written by the publishing worker and read by a test.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// gatedRedis holds every pipeline until the gate opens, like a Redis that
// stopped answering, and counts the pipeline calls it forwards.
type gatedRedis struct {
	next    events.Pipeliner
	started chan struct{}
	gate    chan struct{}
	calls   atomic.Int32
}

func newGatedRedis(next events.Pipeliner) *gatedRedis {
	return &gatedRedis{next: next, started: make(chan struct{}, 100), gate: make(chan struct{})}
}

func (r *gatedRedis) Pipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	r.started <- struct{}{}
	select {
	case <-r.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	r.calls.Add(1)
	return r.next.Pipelined(ctx, fn)
}

func newClient(t *testing.T) *redis.Client {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func subscribe(t *testing.T, client *redis.Client, users ...string) *redis.PubSub {
	t.Helper()
	channels := make([]string, 0, len(users))
	for _, user := range users {
		channels = append(channels, eventbus.UserChannel(user))
	}
	subscription := client.Subscribe(context.Background(), channels...)
	t.Cleanup(func() { _ = subscription.Close() })
	for range users {
		_, err := subscription.Receive(context.Background())
		require.NoError(t, err)
	}
	return subscription
}

func closePublisher(t *testing.T, publisher *events.Publisher) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, publisher.Close(ctx))
}

func memberLeft(recipients ...string) domain.Event {
	return domain.Event{Kind: domain.MemberLeft, RoomID: uuid.NewString(), UserID: uuid.NewString(), Recipients: recipients}
}

func TestPublisher_DeliversToEveryRecipientChannel(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	recipients := []string{uuid.NewString(), uuid.NewString()}
	subscription := subscribe(t, client, recipients...)
	outsider := subscribe(t, client, uuid.NewString())
	publisher := events.NewPublisher(client, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	// The change is committed even if the request ended, so cancellation must not drop it.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	room, user := uuid.NewString(), recipients[1]
	publisher.Notify(canceled, domain.Event{Kind: domain.MemberAdded, RoomID: room, UserID: user, Recipients: recipients})
	closePublisher(t, publisher)
	var channels []string
	for range recipients {
		received, err := subscription.ReceiveMessage(ctx)
		require.NoError(t, err)
		channels = append(channels, received.Channel)
		event := &chatv1.Event{}
		require.NoError(t, proto.Unmarshal([]byte(received.Payload), event))
		require.Equal(t, room, event.GetMemberAdded().GetRoomId())
		require.Equal(t, user, event.GetMemberAdded().GetUserId())
	}
	require.ElementsMatch(t, []string{"chat:user:" + recipients[0], "chat:user:" + recipients[1]}, channels)
	waitCtx, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stop()
	_, err := outsider.ReceiveMessage(waitCtx)
	require.Error(t, err, "a user outside the recipients received the event")
}

func TestPublisher_RedisFailureIsLoggedNotReturned(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	server.Close()
	logs := &syncBuffer{}
	publisher := events.NewPublisher(client, slog.New(slog.NewJSONHandler(logs, nil)))
	publisher.Notify(context.Background(), domain.Event{Kind: "room.renamed", Recipients: []string{uuid.NewString()}})
	require.Contains(t, logs.String(), "encode chat event")
	publisher.Notify(context.Background(), domain.Event{Kind: domain.MemberLeft, RoomID: uuid.NewString()})
	publisher.Notify(context.Background(), memberLeft(uuid.NewString()))
	closePublisher(t, publisher)
	require.Contains(t, logs.String(), "publish chat events failed")
	require.Contains(t, logs.String(), "member.left")
	require.Zero(t, publisher.Dropped())
}

func TestPublisher_NotifyDoesNotWaitForRedis(t *testing.T) {
	gated := newGatedRedis(newClient(t))
	logs := &syncBuffer{}
	publisher := events.NewPublisher(gated, slog.New(slog.NewJSONHandler(logs, nil)), events.WithQueueSize(4), events.WithPublishTimeout(time.Hour))
	t.Cleanup(func() { close(gated.gate); closePublisher(t, publisher) })
	start := time.Now()
	for range 20 {
		publisher.Notify(context.Background(), memberLeft(uuid.NewString()))
	}
	require.Less(t, time.Since(start), time.Second, "requests must not wait for an unresponsive Redis")
}

func TestPublisher_DropsWhenTheQueueIsFull(t *testing.T) {
	client := newClient(t)
	recipient := uuid.NewString()
	subscription := subscribe(t, client, recipient)
	gated := newGatedRedis(client)
	logs := &syncBuffer{}
	publisher := events.NewPublisher(gated, slog.New(slog.NewJSONHandler(logs, nil)), events.WithQueueSize(2))
	first, second, third := memberLeft(recipient), memberLeft(recipient), memberLeft(recipient)
	publisher.Notify(context.Background(), first)
	<-gated.started // the worker holds the first change, so the queue is empty
	publisher.Notify(context.Background(), second)
	publisher.Notify(context.Background(), third)
	for range 3 {
		publisher.Notify(context.Background(), memberLeft(recipient))
	}
	require.Equal(t, uint64(3), publisher.Dropped())
	require.Contains(t, logs.String(), "chat events dropped")
	require.Equal(t, 1, strings.Count(logs.String(), "chat events dropped"), "drops are logged at a limited rate")
	close(gated.gate)
	closePublisher(t, publisher)
	for _, want := range []domain.Event{first, second, third} {
		received, err := subscription.ReceiveMessage(context.Background())
		require.NoError(t, err)
		event := &chatv1.Event{}
		require.NoError(t, proto.Unmarshal([]byte(received.Payload), event))
		require.Equal(t, want.RoomID, event.GetMemberLeft().GetRoomId())
	}
	require.Equal(t, int32(3), gated.calls.Load())
}

func TestPublisher_KeepsOrderAndSendsOneChangeInOnePipeline(t *testing.T) {
	client := newClient(t)
	recipient, other := uuid.NewString(), uuid.NewString()
	subscription := subscribe(t, client, recipient)
	gated := newGatedRedis(client)
	close(gated.gate)
	publisher := events.NewPublisher(gated, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	room := uuid.NewString()
	const sent = 50
	for sequence := int64(1); sequence <= sent; sequence++ {
		message := &domain.Message{ID: uuid.NewString(), RoomID: room, SenderID: recipient, Text: "hi", Sequence: sequence, CreatedAt: time.Now()}
		publisher.Notify(context.Background(), domain.Event{Kind: domain.MessageCreated, RoomID: room, Message: message, Recipients: []string{recipient}})
	}
	// A dialog announces both enrollments of one change together.
	dialog := uuid.NewString()
	publisher.Notify(context.Background(),
		domain.Event{Kind: domain.MemberAdded, RoomID: dialog, UserID: recipient, Recipients: []string{recipient, other}},
		domain.Event{Kind: domain.MemberAdded, RoomID: dialog, UserID: other, Recipients: []string{recipient, other}})
	closePublisher(t, publisher)
	require.Equal(t, int32(sent+1), gated.calls.Load())
	for sequence := int64(1); sequence <= sent; sequence++ {
		received, err := subscription.ReceiveMessage(context.Background())
		require.NoError(t, err)
		event := &chatv1.Event{}
		require.NoError(t, proto.Unmarshal([]byte(received.Payload), event))
		require.Equal(t, sequence, event.GetMessageCreated().GetSequence())
	}
	for _, user := range []string{recipient, other} {
		received, err := subscription.ReceiveMessage(context.Background())
		require.NoError(t, err)
		event := &chatv1.Event{}
		require.NoError(t, proto.Unmarshal([]byte(received.Payload), event))
		require.Equal(t, user, event.GetMemberAdded().GetUserId())
	}
}

func TestPublisher_CloseDrainsQueuedEvents(t *testing.T) {
	client := newClient(t)
	recipient := uuid.NewString()
	subscription := subscribe(t, client, recipient)
	gated := newGatedRedis(client)
	publisher := events.NewPublisher(gated, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	for range 5 {
		publisher.Notify(context.Background(), memberLeft(recipient))
	}
	<-gated.started
	closed := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		closed <- publisher.Close(ctx)
	}()
	close(gated.gate)
	require.NoError(t, <-closed)
	require.Equal(t, int32(5), gated.calls.Load())
	for range 5 {
		_, err := subscription.ReceiveMessage(context.Background())
		require.NoError(t, err)
	}
	// Changes committed after shutdown began are dropped, not published.
	publisher.Notify(context.Background(), memberLeft(recipient))
	require.Equal(t, uint64(1), publisher.Dropped())
	require.NoError(t, publisher.Close(context.Background()), "closing twice is harmless")
}

func TestPublisher_CloseGivesUpAfterItsDeadline(t *testing.T) {
	gated := newGatedRedis(newClient(t))
	publisher := events.NewPublisher(gated, slog.New(slog.NewJSONHandler(io.Discard, nil)), events.WithPublishTimeout(time.Hour))
	for range 3 {
		publisher.Notify(context.Background(), memberLeft(uuid.NewString()))
	}
	<-gated.started
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	require.ErrorIs(t, publisher.Close(ctx), context.DeadlineExceeded)
	require.Less(t, time.Since(start), 5*time.Second)
	require.Equal(t, uint64(3), publisher.Dropped(), "the held change and the queued ones are dropped")
	require.Zero(t, gated.calls.Load())
}

func TestPublisher_MetricsDistinguishRedisAndEncodingFailures(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1, DialTimeout: 10 * time.Millisecond})
	defer client.Close()
	server.Close()
	p := events.NewPublisher(client, slog.New(slog.NewTextHandler(io.Discard, nil)), events.WithQueueSize(4))
	p.Notify(context.Background(), domain.Event{Kind: "invalid", Recipients: []string{uuid.NewString()}})
	p.Notify(context.Background(), memberLeft(uuid.NewString()))
	closePublisher(t, p)
	stats := p.Stats()
	require.Equal(t, uint64(1), stats.Failed)
	require.Equal(t, uint64(1), stats.EncodingFailures)
	require.Zero(t, stats.Published)
	require.Zero(t, stats.Pending)
	require.Equal(t, 4, stats.Capacity)
}
