package handler_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
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
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/realtime"
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

func (nopStream) Subscribe(context.Context, string) error   { return nil }
func (nopStream) Unsubscribe(context.Context, string) error { return nil }

func testOptions() handler.RealtimeOptions {
	return handler.RealtimeOptions{
		AuthTimeout:    200 * time.Millisecond,
		ReauthInterval: 20 * time.Millisecond,
		PingInterval:   time.Hour,
		WriteTimeout:   time.Second,
	}
}

func startRealtime(t *testing.T, hub *realtime.Hub, validator tokens, options handler.RealtimeOptions) string {
	t.Helper()
	server := httptest.NewServer(handler.NewRealtime(hub, validator, options, slog.New(slog.NewJSONHandler(io.Discard, nil))))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
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
