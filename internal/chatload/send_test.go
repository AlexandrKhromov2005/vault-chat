package chatload

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOnlyStoredMessagesExpectDeliveries: a send refused by the Gateway
// produces no event, so its members must not count as missed deliveries.
func TestOnlyStoredMessagesExpectDeliveries(t *testing.T) {
	status := http.StatusTooManyRequests
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(Message{ID: "m", RoomID: "r", ClientID: body["client_message_id"], Sequence: 1})
	}))
	t.Cleanup(gateway.Close)

	r := &Runner{ledger: NewLedger()}
	alice := &user{id: "alice", api: NewAPI(gateway.URL, gateway.Client())}
	bob := &user{id: "bob"}
	alice.conn.Store(&socket{})
	bob.conn.Store(&socket{})
	room := &chatRoom{id: "r", members: []*user{alice, bob}}
	op := operation{user: alice, room: room, clientID: "00000000-0000-4000-8000-000000000001", text: "hello"}

	refused := NewPhaseStats()
	r.send(context.Background(), refused, op)
	require.Equal(t, int64(1), refused.errors["http_429"])
	require.Zero(t, refused.expected.Load())

	status = http.StatusOK
	stored := NewPhaseStats()
	op.clientID = "00000000-0000-4000-8000-000000000002"
	r.send(context.Background(), stored, op)
	require.Equal(t, int64(1), stored.acknowledged.Load())
	require.Equal(t, int64(2), stored.expected.Load())
}
