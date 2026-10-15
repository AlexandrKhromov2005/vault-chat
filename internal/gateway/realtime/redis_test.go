package realtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/ratelimit"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/realtime"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/eventbus"
)

// recordingSink receives what the stream relays.
type recordingSink struct {
	delivered   chan [2]string
	interrupted chan string
}

func newRecordingSink() *recordingSink {
	return &recordingSink{delivered: make(chan [2]string, 16), interrupted: make(chan string, 16)}
}

func (s *recordingSink) Deliver(userID string, payload []byte) {
	s.delivered <- [2]string{userID, string(payload)}
}

func (s *recordingSink) Interrupt(userID string) { s.interrupted <- userID }

func startStream(t *testing.T, server *miniredis.Miniredis) (*realtime.RedisStream, *recordingSink, *redis.Client) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	stream := realtime.NewRedisStream(client, redis.WithChannelHealthCheckInterval(20*time.Millisecond))
	sink := newRecordingSink()
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = stream.Run(ctx, sink)
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
	})
	return stream, sink, client
}

func TestRedisStream_RelaysEventsOfSubscribedUsers(t *testing.T) {
	server := miniredis.RunT(t)
	stream, sink, client := startStream(t, server)
	ctx := context.Background()
	require.NoError(t, stream.Subscribe(ctx, "alice"))
	// Subscribe returned only after Redis confirmed it, so this publish is received.
	require.Equal(t, int64(1), client.Publish(ctx, eventbus.UserChannel("alice"), "event").Val())
	require.Equal(t, [2]string{"alice", "event"}, <-sink.delivered)
	require.Equal(t, int64(0), client.Publish(ctx, eventbus.UserChannel("bob"), "other").Val())
	require.NoError(t, stream.Unsubscribe(ctx, "alice"))
	require.Eventually(t, func() bool {
		return client.Publish(ctx, eventbus.UserChannel("alice"), "late").Val() == 0
	}, time.Second, 10*time.Millisecond)
	require.Empty(t, sink.interrupted)
}

func TestRedisStream_InterruptsUsersAfterReconnecting(t *testing.T) {
	server := miniredis.RunT(t)
	stream, sink, client := startStream(t, server)
	ctx := context.Background()
	require.NoError(t, stream.Subscribe(ctx, "alice"))
	server.Close()
	require.NoError(t, server.Restart())
	// Events published while the connection was down are lost, so the user's
	// connections must be told once the subscription is restored.
	select {
	case user := <-sink.interrupted:
		require.Equal(t, "alice", user)
	case <-time.After(5 * time.Second):
		t.Fatal("reconnection was not reported")
	}
	require.Eventually(t, func() bool {
		return client.Publish(ctx, eventbus.UserChannel("alice"), "after").Val() == 1
	}, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, [2]string{"alice", "after"}, <-sink.delivered)
}

func TestRedisStream_FailedSubscriptionIsNotRestored(t *testing.T) {
	server := miniredis.RunT(t)
	stream, sink, client := startStream(t, server)
	ctx := context.Background()
	server.Close()
	timeout, stop := context.WithTimeout(ctx, 200*time.Millisecond)
	defer stop()
	require.Error(t, stream.Subscribe(timeout, "alice"))
	require.NoError(t, server.Restart())
	require.Eventually(t, func() bool { return stream.Subscribe(ctx, "bob") == nil }, 5*time.Second, 50*time.Millisecond)
	// The caller was told alice is not subscribed, so reconnecting must not
	// resubscribe her behind its back.
	require.Equal(t, int64(0), client.Publish(ctx, eventbus.UserChannel("alice"), "x").Val())
	require.Equal(t, int64(1), client.Publish(ctx, eventbus.UserChannel("bob"), "y").Val())
	require.Equal(t, [2]string{"bob", "y"}, <-sink.delivered)
	require.Empty(t, sink.interrupted)
}

func TestRedisStream_IdlesPastTheClientReadTimeout(t *testing.T) {
	// cmd/gateway shares the rate limiter's client, whose read timeout is a
	// fraction of a second; waiting for events must not be subject to it.
	client := ratelimit.NewRedisClient(miniredis.RunT(t).Addr(), "")
	t.Cleanup(func() { _ = client.Close() })
	stream := realtime.NewRedisStream(client)
	sink := newRecordingSink()
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = stream.Run(ctx, sink)
	}()
	defer func() {
		cancel()
		<-stopped
	}()
	require.NoError(t, stream.Subscribe(ctx, "alice"))
	// Were the read timeout applied, the idle connection would be dropped and
	// resubscribed, which interrupts alice, within twice that timeout.
	require.Never(t, func() bool { return len(sink.interrupted) > 0 }, 2*client.Options().ReadTimeout, 10*time.Millisecond)
	require.Equal(t, int64(1), client.Publish(ctx, eventbus.UserChannel("alice"), "event").Val())
	require.Equal(t, [2]string{"alice", "event"}, <-sink.delivered)
	require.Empty(t, sink.interrupted, "the idle connection was dropped and resubscribed")
}
