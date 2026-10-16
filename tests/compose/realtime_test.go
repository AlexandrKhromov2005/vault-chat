//go:build compose

// Package compose checks the running Compose stack end to end through the
// published gateway port, with the containers wired exactly as deployed.
// Start the stack, then run
//
//	VAULT_CHAT_COMPOSE_GATEWAY_URL=http://127.0.0.1:8080 go test -tags compose -count=1 ./tests/compose/...
//
// Without the variable the tests are skipped. Every run registers new users,
// so the stack may hold data from earlier runs.
package compose

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// gatewayURLEnv names the base URL of the gateway published by Compose.
const gatewayURLEnv = "VAULT_CHAT_COMPOSE_GATEWAY_URL"

var client = &http.Client{Timeout: 10 * time.Second}

func gatewayURL(t *testing.T) string {
	t.Helper()
	base := os.Getenv(gatewayURLEnv)
	if base == "" {
		t.Skipf("%s is not set; start the Compose stack and set it to the gateway URL", gatewayURLEnv)
	}
	return strings.TrimSuffix(base, "/")
}

// waitReady waits until the gateway reaches Auth and Chat.
func waitReady(t *testing.T, base string) {
	t.Helper()
	require.Eventually(t, func() bool {
		resp, err := client.Get(base + "/ready")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, time.Minute, 500*time.Millisecond, "gateway at %s is not ready", base)
}

// post sends a JSON body to the REST API and decodes the JSON answer.
func post(t *testing.T, url, token, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var decoded map[string]any
	if len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, &decoded), string(raw))
	}
	return resp.StatusCode, decoded
}

// signUp registers and logs in a new user and returns its ID and access token.
func signUp(t *testing.T, api, username string) (string, string) {
	t.Helper()
	email := username + "@example.com"
	code, user := post(t, api+"/auth/register", "",
		`{"email":"`+email+`","username":"`+username+`","password":"password1"}`)
	require.Equal(t, http.StatusCreated, code, user)
	code, session := post(t, api+"/auth/login", "", `{"email":"`+email+`","password":"password1"}`)
	require.Equal(t, http.StatusOK, code, session)
	return user["user_id"].(string), session["access_token"].(string)
}

// connect opens a WebSocket, authenticates it in-band and waits for ready.
func connect(t *testing.T, base, token string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/api/v1/ws", nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.CloseNow() })
	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(`{"type":"auth","token":"`+token+`"}`)))
	_, data, err := conn.Read(ctx)
	require.NoError(t, err)
	require.Contains(t, string(data), `"type":"ready"`)
	return conn
}

// TestCompose_MessageReachesPeerOverWebSocket follows a message from the REST
// API through Chat, Redis and the gateway to the peer's WebSocket. It fails
// when Chat publishes to a different Redis than the gateway subscribes to.
func TestCompose_MessageReachesPeerOverWebSocket(t *testing.T) {
	base := gatewayURL(t)
	api := base + "/api/v1"
	waitReady(t, base)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	_, senderToken := signUp(t, api, "sender_"+suffix)
	peerID, peerToken := signUp(t, api, "peer_"+suffix)
	code, room := post(t, api+"/rooms/direct", senderToken, `{"peer_user_id":"`+peerID+`"}`)
	require.Equal(t, http.StatusOK, code, room)
	roomID := room["room_id"].(string)

	conn := connect(t, base, peerToken)
	code, message := post(t, api+"/rooms/"+roomID+"/messages", senderToken,
		`{"client_message_id":"`+uuid.NewString()+`","text":"hello through compose"}`)
	require.Equal(t, http.StatusOK, code, message)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		require.NoError(t, err, "no message.created within 10s; do Chat and the gateway share Redis?")
		var frame map[string]any
		require.NoError(t, json.Unmarshal(data, &frame), string(data))
		// Membership events of the new dialog may arrive first.
		if frame["type"] != "message.created" {
			continue
		}
		require.Equal(t, roomID, frame["room_id"])
		live, ok := frame["message"].(map[string]any)
		require.True(t, ok, string(data))
		require.Equal(t, message["message_id"], live["message_id"])
		require.Equal(t, message["sequence"], live["sequence"])
		return
	}
}
