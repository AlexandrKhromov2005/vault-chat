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
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
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

func testOptions() handler.RealtimeOptions {
	return handler.RealtimeOptions{
		AuthTimeout:    200 * time.Millisecond,
		ReauthInterval: 20 * time.Millisecond,
		WriteTimeout:   time.Second,
	}
}

func startRealtime(t *testing.T, validator tokens, options handler.RealtimeOptions) string {
	t.Helper()
	server := httptest.NewServer(handler.NewRealtime(validator, options, slog.New(slog.NewJSONHandler(io.Discard, nil))))
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

func authenticate(t *testing.T, conn *websocket.Conn, token string) map[string]any {
	t.Helper()
	send(t, conn, `{"type":"auth","token":"`+token+`"}`)
	ready := receive(t, conn)
	require.Equal(t, "ready", ready["type"])
	return ready
}

func TestRealtime_ConfirmsTheAuthenticatedUser(t *testing.T) {
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	url := startRealtime(t, tokens{"alice-token": {"alice", expires}, "bob-token": {"bob", expires}}, testOptions())
	alice, bob := dial(t, url), dial(t, url)
	ready := authenticate(t, alice, "alice-token")
	require.Equal(t, "alice", ready["user_id"])
	require.Equal(t, expires.Format(time.RFC3339), ready["expires_at"])
	require.Equal(t, "bob", authenticate(t, bob, "bob-token")["user_id"])
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
			conn := dial(t, startRealtime(t, valid, testOptions()))
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
	url := startRealtime(t, tokens{"short": {"alice", time.Now().Add(300 * time.Millisecond)}}, testOptions())
	conn := dial(t, url)
	authenticate(t, conn, "short")
	require.Equal(t, websocket.StatusCode(4001), closedWith(t, conn))
}

func TestRealtime_ReauthenticationExtendsTheConnection(t *testing.T) {
	later := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	options := testOptions()
	options.ReauthInterval = 0
	url := startRealtime(t, tokens{"short": {"alice", time.Now().Add(time.Second)}, "renewed": {"alice", later}}, options)
	conn, unrenewed := dial(t, url), dial(t, url)
	authenticate(t, conn, "short")
	authenticate(t, unrenewed, "short")
	ready := authenticate(t, conn, "renewed")
	require.Equal(t, later.Format(time.RFC3339), ready["expires_at"])
	// The connection that kept the short token closes when it expires; the
	// renewed one, which started with the same token, stays open past that.
	require.Equal(t, websocket.StatusCode(4001), closedWith(t, unrenewed))
	authenticate(t, conn, "renewed")
}

func TestRealtime_ReauthenticationKeepsTheIdentityAndPace(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	valid := tokens{"alice-token": {"alice", expires}, "bob-token": {"bob", expires}}
	options := testOptions()
	options.ReauthInterval = 0
	conn := dial(t, startRealtime(t, valid, options))
	authenticate(t, conn, "alice-token")
	send(t, conn, `{"type":"auth","token":"bob-token"}`)
	require.Equal(t, websocket.StatusCode(4001), closedWith(t, conn))
	options.ReauthInterval = time.Hour
	conn = dial(t, startRealtime(t, valid, options))
	authenticate(t, conn, "alice-token")
	send(t, conn, `{"type":"auth","token":"alice-token"}`)
	require.Equal(t, websocket.StatusPolicyViolation, closedWith(t, conn))
}
