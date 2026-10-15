package realtime_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/realtime"
)

// fakeStream records subscription changes in the order they reach Redis.
type fakeStream struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (s *fakeStream) Subscribe(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.calls = append(s.calls, "+"+userID)
	return nil
}

func (s *fakeStream) Unsubscribe(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "-"+userID)
	return nil
}

func (s *fakeStream) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func (s *fakeStream) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func attach(t *testing.T, hub *realtime.Hub, userID string) *realtime.Client {
	t.Helper()
	client, err := hub.Attach(context.Background(), userID)
	require.NoError(t, err)
	return client
}

func requireFrame(t *testing.T, client *realtime.Client, want string) {
	t.Helper()
	select {
	case frame := <-client.Frames():
		require.Equal(t, want, string(frame))
	case <-time.After(time.Second):
		t.Fatalf("no frame %q", want)
	}
}

func requireNoFrame(t *testing.T, client *realtime.Client) {
	t.Helper()
	select {
	case frame := <-client.Frames():
		t.Fatalf("unexpected frame %q", frame)
	default:
	}
}

func requireEnded(t *testing.T, client *realtime.Client, reason realtime.Reason) {
	t.Helper()
	select {
	case <-client.Done():
		require.Equal(t, reason, client.Reason())
	case <-time.After(time.Second):
		t.Fatal("client was not ended")
	}
}

func requireActive(t *testing.T, client *realtime.Client) {
	t.Helper()
	select {
	case <-client.Done():
		t.Fatalf("client ended: %v", client.Reason())
	default:
	}
}

func TestHub_SubscribesWhileTheUserHasClients(t *testing.T) {
	stream := &fakeStream{}
	hub := realtime.NewHub(stream, 3, 4)
	a1, a2, b := attach(t, hub, "a"), attach(t, hub, "a"), attach(t, hub, "b")
	require.Equal(t, []string{"+a", "+b"}, stream.recorded())
	hub.Detach(a1)
	hub.Detach(a1)
	require.Equal(t, []string{"+a", "+b"}, stream.recorded(), "a still has a client")
	hub.Detach(a2)
	require.Equal(t, []string{"+a", "+b", "-a"}, stream.recorded())
	a3 := attach(t, hub, "a")
	require.Equal(t, []string{"+a", "+b", "-a", "+a"}, stream.recorded())
	hub.Detach(a3)
	hub.Detach(b)
	require.Equal(t, []string{"+a", "+b", "-a", "+a", "-a", "-b"}, stream.recorded())
}

func TestHub_DeliversOnlyToTheUsersClients(t *testing.T) {
	hub := realtime.NewHub(&fakeStream{}, 3, 4)
	a1, a2, b := attach(t, hub, "a"), attach(t, hub, "a"), attach(t, hub, "b")
	hub.Deliver("a", []byte("for a"))
	hub.Deliver("nobody", []byte("lost"))
	requireFrame(t, a1, "for a")
	requireFrame(t, a2, "for a")
	requireNoFrame(t, b)
	hub.Detach(a1)
	hub.Deliver("a", []byte("again"))
	requireNoFrame(t, a1)
	requireFrame(t, a2, "again")
}

func TestHub_LimitsConnectionsPerUser(t *testing.T) {
	stream := &fakeStream{}
	hub := realtime.NewHub(stream, 2, 4)
	first := attach(t, hub, "a")
	attach(t, hub, "a")
	_, err := hub.Attach(context.Background(), "a")
	require.ErrorIs(t, err, realtime.ErrTooManyConnections)
	attach(t, hub, "b")
	hub.Detach(first)
	attach(t, hub, "a")
	require.Equal(t, []string{"+a", "+b"}, stream.recorded())
}

func TestHub_EndsAClientThatFallsBehind(t *testing.T) {
	hub := realtime.NewHub(&fakeStream{}, 3, 2)
	slow, fast := attach(t, hub, "a"), attach(t, hub, "a")
	for _, frame := range []string{"1", "2"} {
		hub.Deliver("a", []byte(frame))
		requireFrame(t, fast, frame)
	}
	requireActive(t, slow)
	hub.Deliver("a", []byte("3"))
	requireEnded(t, slow, realtime.ReasonLagging)
	requireFrame(t, fast, "3")
	requireActive(t, fast)
}

func TestHub_InterruptEndsTheUsersClients(t *testing.T) {
	hub := realtime.NewHub(&fakeStream{}, 3, 4)
	a1, a2, b := attach(t, hub, "a"), attach(t, hub, "a"), attach(t, hub, "b")
	hub.Interrupt("a")
	hub.Interrupt("nobody")
	requireEnded(t, a1, realtime.ReasonInterrupted)
	requireEnded(t, a2, realtime.ReasonInterrupted)
	requireActive(t, b)
}

func TestHub_FailedSubscriptionRejectsTheClient(t *testing.T) {
	stream := &fakeStream{}
	hub := realtime.NewHub(stream, 1, 4)
	unavailable := errors.New("redis unavailable")
	stream.fail(unavailable)
	_, err := hub.Attach(context.Background(), "a")
	require.ErrorIs(t, err, unavailable)
	stream.fail(nil)
	// The failed attempt holds neither a connection slot nor a subscription.
	client := attach(t, hub, "a")
	require.Equal(t, []string{"+a"}, stream.recorded())
	hub.Detach(client)
	require.Equal(t, []string{"+a", "-a"}, stream.recorded())
}

func TestHub_ShutdownEndsClientsAndWaitsForThem(t *testing.T) {
	hub := realtime.NewHub(&fakeStream{}, 3, 4)
	a, b := attach(t, hub, "a"), attach(t, hub, "b")
	stopped := make(chan error, 1)
	go func() { stopped <- hub.Shutdown(context.Background()) }()
	requireEnded(t, a, realtime.ReasonShutdown)
	requireEnded(t, b, realtime.ReasonShutdown)
	hub.Detach(a)
	select {
	case err := <-stopped:
		t.Fatalf("shutdown returned before every client detached: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	hub.Detach(b)
	require.NoError(t, <-stopped)
	_, err := hub.Attach(context.Background(), "a")
	require.ErrorIs(t, err, realtime.ErrClosed)
}

func TestHub_ShutdownGivesUpAtItsDeadline(t *testing.T) {
	hub := realtime.NewHub(&fakeStream{}, 3, 4)
	attach(t, hub, "a")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, hub.Shutdown(ctx), context.DeadlineExceeded)
}

func TestHub_ConcurrentClientsKeepSubscriptionsBalanced(t *testing.T) {
	stream := &fakeStream{}
	hub := realtime.NewHub(stream, 100, 4)
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				client, err := hub.Attach(context.Background(), "a")
				if err != nil {
					t.Error(err)
					return
				}
				hub.Deliver("a", []byte("x"))
				hub.Detach(client)
			}
		}()
	}
	wg.Wait()
	calls := stream.recorded()
	require.NotEmpty(t, calls)
	// Subscriptions alternate, so Redis never drops a user who still has clients.
	for i, call := range calls {
		want := "+a"
		if i%2 == 1 {
			want = "-a"
		}
		require.Equal(t, want, call, "call %d of %v", i, calls)
	}
	require.Equal(t, "-a", calls[len(calls)-1])
}
