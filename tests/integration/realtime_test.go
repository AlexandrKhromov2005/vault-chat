package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/events"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/realtime"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/router"
)

// startRealtimeStack serves Auth, a Chat that publishes events to Redis and a
// gateway that relays them from the same Redis over WebSockets, all wired as
// in cmd/chat and cmd/gateway. It returns the REST and WebSocket base URLs.
func startRealtimeStack(t *testing.T) (string, string) {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	redisServer := miniredis.RunT(t)
	newRedis := func() *redis.Client {
		client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
		t.Cleanup(func() { _ = client.Close() })
		return client
	}
	authConn, _ := startAuth(t)
	publisher := events.NewPublisher(newRedis(), logger)
	// Registered before startChat, so it runs after the Chat server stops, and
	// drains the queue like cmd/chat.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, publisher.Close(ctx))
	})
	chatConn, _ := startChat(t, authConn, repository.WithNotifier(publisher))
	stream := realtime.NewRedisStream(newRedis())
	hub := realtime.NewHub(stream, 5, 64)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = stream.Run(ctx, hub)
	}()
	deps := gatewayDeps(t, authConn, redisServer.Addr(), 1000, chatConn)
	websockets := handler.NewRealtime(hub, authv1.NewAuthServiceClient(authConn), handler.RealtimeOptions{
		AuthTimeout: 5 * time.Second, ReauthInterval: time.Second, PingInterval: time.Minute, WriteTimeout: 5 * time.Second,
		MaxConnections: 100, MaxUnauthenticated: 10,
	}, logger)
	deps.Realtime = websockets
	gateway := httptest.NewServer(router.New(deps))
	t.Cleanup(func() {
		shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		require.NoError(t, websockets.Shutdown(shutdown))
		require.NoError(t, hub.Shutdown(shutdown))
		gateway.Close()
		cancel()
		<-stopped
	})
	return gateway.URL + "/api/v1", "ws" + strings.TrimPrefix(gateway.URL, "http") + "/api/v1/ws"
}

// connect opens an authenticated WebSocket and waits for its ready frame.
func connect(t *testing.T, url, token string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.CloseNow() })
	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(`{"type":"auth","token":"`+token+`"}`)))
	require.Equal(t, "ready", next(t, conn)["type"])
	return conn
}

// next returns the next frame of conn.
func next(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	require.NoError(t, err)
	var frame map[string]any
	require.NoError(t, json.Unmarshal(data, &frame))
	return frame
}

func TestRealtime_MembersReceiveEventsAndCatchUpAfterReconnecting(t *testing.T) {
	api, ws := startRealtimeStack(t)
	var ids []string
	var credentials []tokens
	for _, name := range []string{"owner", "member", "removed", "outsider"} {
		code, body := call(t, http.MethodPost, api+"/auth/register", "", fmt.Sprintf(`{"email":"%s@example.com","username":"%s","password":"password1"}`, name, name))
		require.Equal(t, 201, code, body)
		ids = append(ids, body["user_id"].(string))
		credentials = append(credentials, login(t, api+"/auth", name+"@example.com"))
	}
	owner, member, removed, outsider := credentials[0], credentials[1], credentials[2], credentials[3]
	code, channel := call(t, http.MethodPost, api+"/rooms/channels", owner.access, `{"name":"live","private":true}`)
	require.Equal(t, 201, code, channel)
	roomID := channel["room_id"].(string)
	room := api + "/rooms/" + roomID
	for _, id := range ids[1:3] {
		code, _ = call(t, http.MethodPut, room+"/members/"+id, owner.access, "")
		require.Equal(t, 204, code)
	}
	send := func(text string) map[string]any {
		t.Helper()
		code, message := call(t, http.MethodPost, room+"/messages", owner.access, `{"client_message_id":"`+uuid.NewString()+`","text":"`+text+`"}`)
		require.Equal(t, 200, code, message)
		return message
	}
	memberWS, removedWS, outsiderWS := connect(t, ws, member.access), connect(t, ws, removed.access), connect(t, ws, outsider.access)

	first := send("first")
	for _, conn := range []*websocket.Conn{memberWS, removedWS} {
		frame := next(t, conn)
		require.Equal(t, "message.created", frame["type"])
		require.Equal(t, roomID, frame["room_id"])
		// Live messages have exactly the REST representation.
		require.Equal(t, first, frame["message"])
	}
	// The outsider's first event is about its own dialog, not the channel.
	code, outsiderDialog := call(t, http.MethodPost, api+"/rooms/direct", owner.access, `{"peer_user_id":"`+ids[3]+`"}`)
	require.Equal(t, 200, code, outsiderDialog)
	frame := next(t, outsiderWS)
	require.Equal(t, "member.added", frame["type"])
	require.Equal(t, outsiderDialog["room_id"], frame["room_id"])

	code, _ = call(t, http.MethodDelete, room+"/members/"+ids[2], owner.access, "")
	require.Equal(t, 204, code)
	for _, conn := range []*websocket.Conn{memberWS, removedWS} {
		require.Equal(t, map[string]any{"type": "member.removed", "room_id": roomID, "user_id": ids[2]}, next(t, conn))
	}
	second := send("second")
	require.Equal(t, second, next(t, memberWS)["message"])
	// The removed user's next event is a new dialog, not the channel's message.
	code, removedDialog := call(t, http.MethodPost, api+"/rooms/direct", owner.access, `{"peer_user_id":"`+ids[2]+`"}`)
	require.Equal(t, 200, code, removedDialog)
	frame = next(t, removedWS)
	require.Equal(t, "member.added", frame["type"])
	require.Equal(t, removedDialog["room_id"], frame["room_id"])

	// While disconnected the member misses two messages and fetches exactly
	// those after the last sequence it saw.
	require.NoError(t, memberWS.Close(websocket.StatusNormalClosure, ""))
	missed := []any{send("third"), send("fourth")}
	code, page := call(t, http.MethodGet, fmt.Sprintf("%s/messages?after=%v", room, second["sequence"]), member.access, "")
	require.Equal(t, 200, code, page)
	require.Equal(t, missed, page["messages"])
	require.Empty(t, page["next_cursor"])
	memberWS = connect(t, ws, member.access)
	fifth := send("fifth")
	require.Equal(t, fifth, next(t, memberWS)["message"])
	require.EqualValues(t, 5, fifth["sequence"])
}

func TestRealtime_RequiresAnAccessToken(t *testing.T) {
	api, ws := startRealtimeStack(t)
	code, body := call(t, http.MethodPost, api+"/auth/register", "", `{"email":"user@example.com","username":"user","password":"password1"}`)
	require.Equal(t, 201, code, body)
	session := login(t, api+"/auth", "user@example.com")
	for _, token := range []string{session.refresh, "forged"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		conn, _, err := websocket.Dial(ctx, ws, nil)
		require.NoError(t, err)
		require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(`{"type":"auth","token":"`+token+`"}`)))
		_, _, err = conn.Read(ctx)
		require.Equal(t, websocket.StatusCode(4001), websocket.CloseStatus(err))
		cancel()
	}
	// A token in the URL is ignored: the connection still has to authenticate.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, ws+"?access_token="+session.access, nil)
	require.NoError(t, err)
	_, _, err = conn.Read(ctx)
	require.Equal(t, websocket.StatusCode(4001), websocket.CloseStatus(err))
}
