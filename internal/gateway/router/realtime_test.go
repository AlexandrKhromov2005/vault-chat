package router_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	handlermocks "github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler/mocks"
	middlewaremocks "github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware/mocks"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/ratelimit"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/realtime"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/router"
)

type nopStream struct{}

func (nopStream) Subscribe(context.Context, string) error   { return nil }
func (nopStream) Unsubscribe(context.Context, string) error { return nil }

// startRealtimeRouter serves the whole router with a request budget far
// shorter than the tests keep a WebSocket open.
func startRealtimeRouter(t *testing.T, limiter *middlewaremocks.MockLimiter) (string, *realtime.Hub, *middlewaremocks.MockTokenValidator) {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	validator := middlewaremocks.NewMockTokenValidator(t)
	hub := realtime.NewHub(nopStream{}, 5, 8)
	server := httptest.NewServer(router.New(router.Deps{
		Auth:           handler.NewAuthHandler(handlermocks.NewMockAuthClient(t), logger),
		Health:         handler.NewHealth(nil, logger),
		TokenValidator: validator,
		Limiter:        limiter,
		Logger:         logger,
		MaxBodyBytes:   64,
		RequestTimeout: budget,
		Realtime: handler.NewRealtime(hub, validator, handler.RealtimeOptions{
			AuthTimeout: time.Second, ReauthInterval: time.Second, PingInterval: time.Hour, WriteTimeout: time.Second,
		}, logger),
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws", hub, validator
}

func TestRouter_WebSocketOutlivesTheRequestBudget(t *testing.T) {
	limiter := middlewaremocks.NewMockLimiter(t)
	limiter.EXPECT().Allow(mock.Anything, "ip:127.0.0.1").Return(ratelimit.Result{Allowed: true, Limit: 30, Remaining: 29}, nil).Once()
	url, hub, validator := startRealtimeRouter(t, limiter)
	validator.EXPECT().ValidateToken(mock.Anything, &authv1.ValidateTokenRequest{Token: "tok"}).
		Return(&authv1.ValidateTokenResponse{Valid: true, UserId: "u-1", ExpiresAt: timestamppb.New(time.Now().Add(time.Hour))}, nil).Once()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, nil)
	require.NoError(t, err)
	defer func() { _ = conn.CloseNow() }()
	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(`{"type":"auth","token":"tok"}`)))
	_, ready, err := conn.Read(ctx)
	require.NoError(t, err)
	require.Contains(t, string(ready), `"ready"`)
	// The API request timeout must not end a WebSocket connection.
	time.Sleep(10 * budget)
	payload, err := proto.Marshal(&chatv1.Event{Kind: &chatv1.Event_MemberAdded{MemberAdded: &chatv1.MemberChange{RoomId: "r", UserId: "u-1"}}})
	require.NoError(t, err)
	hub.Deliver("u-1", payload)
	_, frame, err := conn.Read(ctx)
	require.NoError(t, err)
	require.Contains(t, string(frame), `"member.added"`)
}

func TestRouter_WebSocketUpgradesAreRateLimitedPerIP(t *testing.T) {
	limiter := middlewaremocks.NewMockLimiter(t)
	limiter.EXPECT().Allow(mock.Anything, "ip:127.0.0.1").Return(ratelimit.Result{Allowed: false, Limit: 30, RetryAfter: time.Second}, nil).Once()
	url, _, _ := startRealtimeRouter(t, limiter)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, url, nil)
	require.Error(t, err)
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
}
