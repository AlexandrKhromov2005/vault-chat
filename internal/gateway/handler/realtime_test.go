package handler_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/realtime"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/eventbus"
)

// tokens maps access tokens to their owners and expiry; "down" fails like an
// unreachable Auth, and unknown tokens are invalid.
type tokens map[string]struct {
	user    string
	expires time.Time
}

func (v tokens) ValidateToken(_ context.Context, in *authv1.ValidateTokenRequest, _ ...grpc.CallOption) (*authv1.ValidateTokenResponse, error) {
	if in.GetToken() == "down" {
		return nil, status.Error(codes.Unavailable, "auth unavailable")
	}
	owner, ok := v[in.GetToken()]
	if !ok {
		return &authv1.ValidateTokenResponse{}, nil
	}
	return &authv1.ValidateTokenResponse{Valid: true, UserId: owner.user, ExpiresAt: timestamppb.New(owner.expires)}, nil
}

// nopStream subscribes instantly; tests deliver events through the hub.
type nopStream struct{}

type revocableToken struct{ revoked atomic.Bool }

func (v *revocableToken) ValidateToken(context.Context, *authv1.ValidateTokenRequest, ...grpc.CallOption) (*authv1.ValidateTokenResponse, error) {
	return &authv1.ValidateTokenResponse{Valid: !v.revoked.Load(), UserId: "alice", ExpiresAt: timestamppb.New(time.Now().Add(time.Hour))}, nil
}

func TestRealtimeRevocationClosesIdleConnectionsAndBlocksEvents(t *testing.T) {
	for _, mode := range []string{"event", "hint", "lost hint"} {
		t.Run(mode, func(t *testing.T) {
			hub := realtime.NewHub(nopStream{}, 5, 8)
			validator := &revocableToken{}
			options := testOptions()
			if mode == "lost hint" {
				options.SessionCheckInterval = 10 * time.Millisecond
			}
			_, url := startRealtimeHandler(t, hub, validator, options)
			conn := dial(t, url)
			authenticate(t, conn, "token")
			validator.revoked.Store(true)
			if mode == "hint" {
				hub.Deliver("alice", []byte(eventbus.SessionRevoked))
			}
			if mode == "event" {
				hub.Deliver("alice", eventPayload(t, &chatv1.Event{Kind: &chatv1.Event_MemberLeft{MemberLeft: &chatv1.MemberChange{RoomId: "secret", UserId: "alice"}}}))
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, _, err := conn.Read(ctx)
			require.Equal(t, websocket.StatusCode(4001), websocket.CloseStatus(err), "no event may pass after session revocation")
		})
	}
}

func (nopStream) Subscribe(context.Context, string) error   { return nil }
func (nopStream) Unsubscribe(context.Context, string) error { return nil }

// blockingValidator holds every call until its context ends, reporting on
// entered that a call is waiting.
type blockingValidator struct{ entered chan struct{} }

func (v blockingValidator) ValidateToken(ctx context.Context, _ *authv1.ValidateTokenRequest, _ ...grpc.CallOption) (*authv1.ValidateTokenResponse, error) {
	v.entered <- struct{}{}
	<-ctx.Done()
	return nil, status.FromContextError(ctx.Err()).Err()
}

// blockingStream holds every subscription until its context ends.
type blockingStream struct{ entered chan struct{} }

func (s blockingStream) Subscribe(ctx context.Context, _ string) error {
	s.entered <- struct{}{}
	<-ctx.Done()
	return ctx.Err()
}

func (blockingStream) Unsubscribe(context.Context, string) error { return nil }

func testOptions() handler.RealtimeOptions {
	return handler.RealtimeOptions{
		AllowedOrigins: []string{"https://app.example"},
		AuthTimeout:    200 * time.Millisecond,
		ReauthInterval: 20 * time.Millisecond,
		PingInterval:   time.Hour,
		WriteTimeout:   time.Second,
	}
}

func startRealtime(t *testing.T, hub *realtime.Hub, validator tokens, options handler.RealtimeOptions) string {
	t.Helper()
	_, url := startRealtimeHandler(t, hub, validator, options)
	return url
}

// startRealtimeHandler serves the endpoint and also returns its handler.
func startRealtimeHandler(t *testing.T, hub *realtime.Hub, validator middleware.TokenValidator, options handler.RealtimeOptions) (*handler.Realtime, string) {
	t.Helper()
	h := handler.NewRealtime(hub, validator, options, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	return h, "ws" + strings.TrimPrefix(server.URL, "http")
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

func send(t *testing.T, conn *websocket.Conn, frame string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(frame)))
}

func receive(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	kind, data, err := conn.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, websocket.MessageText, kind)
	var frame map[string]any
	require.NoError(t, json.Unmarshal(data, &frame))
	return frame
}

// closedWith reads until the server closes the connection and returns its code.
func closedWith(t *testing.T, conn *websocket.Conn) websocket.StatusCode {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			require.NoError(t, ctx.Err(), "the server did not close the connection")
			return websocket.CloseStatus(err)
		}
	}
}

// tryReady connects and authenticates with token, reporting failures instead
// of failing the test, so that it can run inside require.Eventually. A
// connection that got its ready frame stays open until the test ends.
func tryReady(t *testing.T, url, token string) (*websocket.Conn, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, false
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"auth","token":"`+token+`"}`)); err != nil {
		_ = conn.CloseNow()
		return nil, false
	}
	_, data, err := conn.Read(ctx)
	if err != nil || !strings.Contains(string(data), `"ready"`) {
		_ = conn.CloseNow()
		return nil, false
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn, true
}

func authenticate(t *testing.T, conn *websocket.Conn, token string) map[string]any {
	t.Helper()
	send(t, conn, `{"type":"auth","token":"`+token+`"}`)
	ready := receive(t, conn)
	require.Equal(t, "ready", ready["type"])
	return ready
}

func eventPayload(t *testing.T, event *chatv1.Event) []byte {
	t.Helper()
	payload, err := proto.Marshal(event)
	require.NoError(t, err)
	return payload
}

func TestRealtime_DeliversTheAuthenticatedUsersEvents(t *testing.T) {
	hub := realtime.NewHub(nopStream{}, 5, 8)
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	url := startRealtime(t, hub, tokens{"alice-token": {"alice", expires}, "bob-token": {"bob", expires}}, testOptions())
	alice, bob := dial(t, url), dial(t, url)
	ready := authenticate(t, alice, "alice-token")
	require.Equal(t, "alice", ready["user_id"])
	require.Equal(t, expires.Format(time.RFC3339), ready["expires_at"])
	authenticate(t, bob, "bob-token")
	message := &chatv1.Message{Id: "m", RoomId: "r", SenderId: "bob", ClientMessageId: "c", Text: "hi", Sequence: 4, CreatedAt: timestamppb.New(expires)}
	hub.Deliver("alice", eventPayload(t, &chatv1.Event{Kind: &chatv1.Event_MessageCreated{MessageCreated: message}}))
	hub.Deliver("alice", eventPayload(t, &chatv1.Event{}))
	hub.Deliver("alice", []byte{0xff})
	hub.Deliver("alice", eventPayload(t, &chatv1.Event{Kind: &chatv1.Event_MemberRemoved{MemberRemoved: &chatv1.MemberChange{RoomId: "r", UserId: "alice"}}}))
	frame := receive(t, alice)
	require.Equal(t, "message.created", frame["type"])
	require.Equal(t, "r", frame["room_id"])
	require.EqualValues(t, 4, frame["message"].(map[string]any)["sequence"])
	// Unknown and undecodable events are skipped without closing the connection.
	require.Equal(t, map[string]any{"type": "member.removed", "room_id": "r", "user_id": "alice"}, receive(t, alice))
	hub.Deliver("bob", eventPayload(t, &chatv1.Event{Kind: &chatv1.Event_MemberLeft{MemberLeft: &chatv1.MemberChange{RoomId: "q", UserId: "bob"}}}))
	require.Equal(t, "member.left", receive(t, bob)["type"])
}

func TestRealtime_RejectsConnectionsWithoutValidAuthentication(t *testing.T) {
	valid := tokens{"alice-token": {"alice", time.Now().Add(time.Hour)}, "expired": {"alice", time.Now().Add(-time.Second)}}
	for _, tc := range []struct {
		name  string
		frame string
		kind  websocket.MessageType
		code  websocket.StatusCode
	}{
		{"no auth frame in time", "", websocket.MessageText, 4001},
		{"invalid token", `{"type":"auth","token":"forged"}`, websocket.MessageText, 4001},
		{"expired token", `{"type":"auth","token":"expired"}`, websocket.MessageText, 4001},
		{"malformed frame", `{"type":"auth"}`, websocket.MessageText, websocket.StatusPolicyViolation},
		{"binary frame", `{"type":"auth","token":"alice-token"}`, websocket.MessageBinary, websocket.StatusUnsupportedData},
		{"auth unavailable", `{"type":"auth","token":"down"}`, websocket.MessageText, websocket.StatusTryAgainLater},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := dial(t, startRealtime(t, realtime.NewHub(nopStream{}, 5, 8), valid, testOptions()))
			if tc.frame != "" {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				require.NoError(t, conn.Write(ctx, tc.kind, []byte(tc.frame)))
			}
			require.Equal(t, tc.code, closedWith(t, conn))
		})
	}
}

func TestRealtime_ClosesWhenTheTokenExpires(t *testing.T) {
	url := startRealtime(t, realtime.NewHub(nopStream{}, 5, 8), tokens{"short": {"alice", time.Now().Add(300 * time.Millisecond)}}, testOptions())
	conn := dial(t, url)
	authenticate(t, conn, "short")
	require.Equal(t, websocket.StatusCode(4001), closedWith(t, conn))
}

func TestRealtime_ReauthenticationExtendsTheConnection(t *testing.T) {
	hub := realtime.NewHub(nopStream{}, 5, 8)
	later := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	options := testOptions()
	options.ReauthInterval = 0
	url := startRealtime(t, hub, tokens{"short": {"alice", time.Now().Add(time.Second)}, "renewed": {"alice", later}}, options)
	conn, unrenewed := dial(t, url), dial(t, url)
	authenticate(t, conn, "short")
	authenticate(t, unrenewed, "short")
	ready := authenticate(t, conn, "renewed")
	require.Equal(t, later.Format(time.RFC3339), ready["expires_at"])
	// The connection that kept the short token closes when it expires; the
	// renewed one, which started with the same token, stays open past that.
	require.Equal(t, websocket.StatusCode(4001), closedWith(t, unrenewed))
	hub.Deliver("alice", eventPayload(t, &chatv1.Event{Kind: &chatv1.Event_MemberAdded{MemberAdded: &chatv1.MemberChange{RoomId: "r", UserId: "alice"}}}))
	require.Equal(t, "member.added", receive(t, conn)["type"])
}

func TestRealtime_ReauthenticationKeepsTheIdentityAndPace(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	valid := tokens{"alice-token": {"alice", expires}, "bob-token": {"bob", expires}}
	options := testOptions()
	options.ReauthInterval = 0
	conn := dial(t, startRealtime(t, realtime.NewHub(nopStream{}, 5, 8), valid, options))
	authenticate(t, conn, "alice-token")
	send(t, conn, `{"type":"auth","token":"bob-token"}`)
	require.Equal(t, websocket.StatusCode(4001), closedWith(t, conn))
	options.ReauthInterval = time.Hour
	conn = dial(t, startRealtime(t, realtime.NewHub(nopStream{}, 5, 8), valid, options))
	authenticate(t, conn, "alice-token")
	send(t, conn, `{"type":"auth","token":"alice-token"}`)
	require.Equal(t, websocket.StatusPolicyViolation, closedWith(t, conn))
}

func TestRealtime_ChecksTheOriginBeforeUpgrading(t *testing.T) {
	for _, tc := range []struct {
		name    string
		allowed []string
		origin  string
		ok      bool
	}{
		{"listed origin", []string{"https://app.example"}, "https://app.example", true},
		{"no origin from a non-browser client", []string{"https://app.example"}, "", true},
		{"same origin", nil, "same", true},
		{"unlisted origin", []string{"https://app.example"}, "https://evil.example", false},
		{"listed host with another scheme", []string{"https://app.example"}, "http://app.example", false},
		{"no origins configured", nil, "https://app.example", false},
		{"any origin", []string{"*"}, "https://evil.example", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := testOptions()
			options.AllowedOrigins = tc.allowed
			url := startRealtime(t, realtime.NewHub(nopStream{}, 5, 8), tokens{}, options)
			header := http.Header{}
			switch tc.origin {
			case "":
			case "same":
				header.Set("Origin", "http"+strings.TrimPrefix(url, "ws"))
			default:
				header.Set("Origin", tc.origin)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
			if tc.ok {
				require.NoError(t, err)
				_ = conn.CloseNow()
				return
			}
			require.Error(t, err)
			require.Equal(t, http.StatusForbidden, resp.StatusCode)
		})
	}
}

func TestRealtime_HubEndsConnections(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	valid := tokens{"alice-token": {"alice", expires}}
	hub := realtime.NewHub(nopStream{}, 1, 8)
	url := startRealtime(t, hub, valid, testOptions())
	first := dial(t, url)
	authenticate(t, first, "alice-token")
	second := dial(t, url)
	send(t, second, `{"type":"auth","token":"alice-token"}`)
	require.Equal(t, websocket.StatusTryAgainLater, closedWith(t, second), "over the per-user limit")
	hub.Interrupt("alice")
	require.Equal(t, websocket.StatusTryAgainLater, closedWith(t, first), "events may have been lost")
	// The interrupted connection released its slot.
	var third *websocket.Conn
	require.Eventually(t, func() bool {
		var ok bool
		third, ok = tryReady(t, url, "alice-token")
		return ok
	}, 3*time.Second, 50*time.Millisecond)
	shutdown := make(chan error, 1)
	go func() { shutdown <- hub.Shutdown(context.Background()) }()
	require.Equal(t, websocket.StatusGoingAway, closedWith(t, third))
	require.NoError(t, <-shutdown)
}

func TestRealtime_PingsDropUnresponsiveClients(t *testing.T) {
	options := testOptions()
	options.PingInterval = 20 * time.Millisecond
	options.WriteTimeout = 100 * time.Millisecond
	hub := realtime.NewHub(nopStream{}, 1, 8)
	url := startRealtime(t, hub, tokens{"alice-token": {"alice", time.Now().Add(time.Hour)}}, options)
	responsive := dial(t, url)
	authenticate(t, responsive, "alice-token")
	// Reading answers pings; a responsive client outlives many ping intervals.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := responsive.Read(ctx)
	require.Error(t, err)
	require.GreaterOrEqual(t, time.Since(start), 250*time.Millisecond, "the server dropped a responsive client: %v", err)
	// The expired read closed the client; once the server notices, the slot is
	// free for a client that then stops reading and so stops answering pings.
	requireReady(t, url)
	// The server drops the silent connection, which frees the slot again.
	requireReady(t, url)
}

// requireReady eventually authenticates a new connection of alice and leaves it open.
func requireReady(t *testing.T, url string) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, ok := tryReady(t, url, "alice-token")
		return ok
	}, 3*time.Second, 50*time.Millisecond)
}

func TestRealtime_ShutdownClosesEveryConnectionWithGoingAway(t *testing.T) {
	options := testOptions()
	options.AuthTimeout = time.Hour
	h, url := startRealtimeHandler(t, realtime.NewHub(nopStream{}, 5, 8), tokens{"alice-token": {"alice", time.Now().Add(time.Hour)}}, options)
	authenticated, pending := dial(t, url), dial(t, url)
	authenticate(t, authenticated, "alice-token")
	shutdown := make(chan error, 1)
	go func() { shutdown <- h.Shutdown(context.Background()) }()
	// The clients are not reading yet, so their close handshakes cannot
	// complete and Shutdown must keep waiting for them.
	require.Never(t, func() bool { return len(shutdown) > 0 }, 300*time.Millisecond, 10*time.Millisecond)
	require.Equal(t, websocket.StatusGoingAway, closedWith(t, authenticated))
	require.Equal(t, websocket.StatusGoingAway, closedWith(t, pending), "a connection that has not authenticated yet")
	select {
	case err := <-shutdown:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Shutdown did not return after the connections closed")
	}
	// New connections are refused before upgrading.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, url, nil)
	require.Error(t, err)
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	require.NotEmpty(t, resp.Header.Get("Retry-After"))
}

func TestRealtime_ShutdownGivesUpWhenTheContextEnds(t *testing.T) {
	options := testOptions()
	options.AuthTimeout = time.Hour
	h, url := startRealtimeHandler(t, realtime.NewHub(nopStream{}, 5, 8), tokens{}, options)
	conn := dial(t, url)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	// The client never answers the close frame.
	require.ErrorIs(t, h.Shutdown(ctx), context.DeadlineExceeded)
	require.Equal(t, websocket.StatusGoingAway, closedWith(t, conn))
}

// requireRefused checks that an upgrade is refused before it happens.
func requireRefused(t *testing.T, url string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, url, nil)
	require.Error(t, err)
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	require.NotEmpty(t, resp.Header.Get("Retry-After"))
}

func TestRealtime_LimitsConcurrentConnections(t *testing.T) {
	options := testOptions()
	options.AuthTimeout = time.Hour
	options.MaxConnections = 2
	options.MaxUnauthenticated = 1
	expires := time.Now().Add(time.Hour)
	url := startRealtime(t, realtime.NewHub(nopStream{}, 5, 8), tokens{"alice-token": {"alice", expires}, "bob-token": {"bob", expires}}, options)
	alice := dial(t, url)
	requireRefused(t, url) // alice has not authenticated yet
	authenticate(t, alice, "alice-token")
	bob := dial(t, url)
	authenticate(t, bob, "bob-token")
	requireRefused(t, url) // two connections are open
	require.NoError(t, alice.Close(websocket.StatusNormalClosure, ""))
	// The closed connection frees its slot for anyone.
	require.Eventually(t, func() bool {
		_, ok := tryReady(t, url, "bob-token")
		return ok
	}, 3*time.Second, 20*time.Millisecond)
}

func TestRealtime_FailedAuthenticationFreesItsSlot(t *testing.T) {
	options := testOptions()
	options.MaxConnections = 1
	options.MaxUnauthenticated = 1
	url := startRealtime(t, realtime.NewHub(nopStream{}, 5, 8), tokens{"alice-token": {"alice", time.Now().Add(time.Hour)}}, options)
	conn := dial(t, url)
	send(t, conn, `{"type":"auth","token":"forged"}`)
	require.Equal(t, websocket.StatusCode(4001), closedWith(t, conn))
	require.Eventually(t, func() bool {
		_, ok := tryReady(t, url, "alice-token")
		return ok
	}, 3*time.Second, 20*time.Millisecond)
}

func TestRealtime_ShutdownInterruptsAuthentication(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	for _, tc := range []struct {
		name      string
		hub       func(entered chan struct{}) *realtime.Hub
		validator func(entered chan struct{}) middleware.TokenValidator
	}{
		{
			name: "token check in progress",
			hub:  func(chan struct{}) *realtime.Hub { return realtime.NewHub(nopStream{}, 5, 8) },
			validator: func(entered chan struct{}) middleware.TokenValidator {
				return blockingValidator{entered: entered}
			},
		},
		{
			name: "subscription in progress",
			hub: func(entered chan struct{}) *realtime.Hub {
				return realtime.NewHub(blockingStream{entered: entered}, 5, 8)
			},
			validator: func(chan struct{}) middleware.TokenValidator {
				return tokens{"alice-token": {"alice", expires}}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The limits would only end these calls after an hour.
			options := testOptions()
			options.AuthTimeout = time.Hour
			entered := make(chan struct{}, 1)
			h, url := startRealtimeHandler(t, tc.hub(entered), tc.validator(entered), options)
			conn := dial(t, url)
			send(t, conn, `{"type":"auth","token":"alice-token"}`)
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("authentication did not start")
			}
			shutdown := make(chan error, 1)
			go func() { shutdown <- h.Shutdown(context.Background()) }()
			require.Equal(t, websocket.StatusGoingAway, closedWith(t, conn))
			select {
			case err := <-shutdown:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("Shutdown waited for the authentication to finish")
			}
		})
	}
}

// heldWrites is a client connection whose writes wait from hold until resume,
// reporting on held that one is waiting.
type heldWrites struct {
	net.Conn
	holding atomic.Bool
	held    chan struct{}
	resumed chan struct{}
	once    sync.Once
}

func (c *heldWrites) hold() { c.holding.Store(true) }

func (c *heldWrites) resume() { c.once.Do(func() { close(c.resumed) }) }

func (c *heldWrites) Write(p []byte) (int, error) {
	if c.holding.Load() {
		select {
		case c.held <- struct{}{}:
		default:
		}
		<-c.resumed
	}
	return c.Conn.Write(p)
}

// dialHeld connects through a heldWrites, so that the test decides when the
// server receives what the client writes.
func dialHeld(t *testing.T, url string) (*websocket.Conn, *heldWrites) {
	t.Helper()
	writes := &heldWrites{held: make(chan struct{}, 1), resumed: make(chan struct{})}
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		writes.Conn = conn
		return writes, nil
	}}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: client})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.CloseNow() })
	// Cleanups run last to first: a held write must end before CloseNow,
	// which waits for it.
	t.Cleanup(writes.resume)
	return conn, writes
}

// await fails the test unless ch is ready within a few seconds.
func await(t *testing.T, ch <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal(failure)
	}
}

func TestRealtime_ConnectionHoldsItsSlotUntilClosed(t *testing.T) {
	options := testOptions()
	options.AuthTimeout = 50 * time.Millisecond
	options.MaxConnections = 1
	h := handler.NewRealtime(realtime.NewHub(nopStream{}, 5, 8), tokens{}, options, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	// served is closed when the handler of the first connection returns, by
	// which time the connection has given up its slot.
	served := make(chan struct{})
	var first sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isFirst := false
		first.Do(func() { isFirst = true })
		h.ServeHTTP(w, r)
		if isFirst {
			close(served)
		}
	}))
	t.Cleanup(server.Close)
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, writes := dialHeld(t, url)
	writes.hold()
	// The authentication times out and the server sends its close frame; the
	// client reads it, but its answer is held, so the close handshake goes on.
	code := make(chan websocket.StatusCode, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _, err := conn.Read(ctx)
		code <- websocket.CloseStatus(err)
	}()
	await(t, writes.held, "the client did not answer a close frame")
	requireRefused(t, url)
	writes.resume()
	require.Equal(t, websocket.StatusCode(4001), <-code)
	// Once the handshake ends and the handler returns, the slot admits a new
	// connection at once.
	await(t, served, "the handler did not return after the close handshake")
	dial(t, url)
}

func TestRealtime_MetricsTrackPendingAndClosedConnections(t *testing.T) {
	h, url := startRealtimeHandler(t, realtime.NewHub(nopStream{}, 5, 8), tokens{"token": {"alice", time.Now().Add(time.Hour)}}, testOptions())
	c := dial(t, url)
	open, pending := h.ConnectionCounts()
	require.Equal(t, 1, open)
	require.Equal(t, 1, pending)
	authenticate(t, c, "token")
	require.Eventually(t, func() bool { open, pending = h.ConnectionCounts(); return open == 1 && pending == 0 }, time.Second, time.Millisecond)
	require.NoError(t, c.Close(websocket.StatusNormalClosure, ""))
	require.Eventually(t, func() bool { open, pending = h.ConnectionCounts(); return open == 0 && pending == 0 }, time.Second, time.Millisecond)
}
