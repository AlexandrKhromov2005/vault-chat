package integration

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
)

func TestChatRoomListAPI_ActivityOrderPeersAndIsolation(t *testing.T) {
	authConn, _ := startAuth(t)
	chatConn, _ := startChat(t, authConn)
	gateway := startGateway(t, authConn, 1000, chatConn)
	api := gateway.URL + "/api/v1"
	var ids []string
	var credentials []tokens
	for _, name := range []string{"lister", "peer", "inviter"} {
		code, body := call(t, http.MethodPost, api+"/auth/register", "", fmt.Sprintf(`{"email":"%s@example.com","username":"%s","password":"password1"}`, name, name))
		require.Equal(t, 201, code, body)
		ids = append(ids, body["user_id"].(string))
		credentials = append(credentials, login(t, api+"/auth", name+"@example.com"))
	}
	a, b, c := credentials[0], credentials[1], credentials[2]
	code, page := call(t, http.MethodGet, api+"/rooms", a.access, "")
	require.Equal(t, 200, code, page)
	require.Empty(t, page["rooms"])
	require.Empty(t, page["next_cursor"])
	code, direct := call(t, http.MethodPost, api+"/rooms/direct", a.access, `{"peer_user_id":"`+ids[1]+`"}`)
	require.Equal(t, 200, code, direct)
	peers := []any{ids[0], ids[1]}
	sort.Slice(peers, func(i, j int) bool { return peers[i].(string) < peers[j].(string) })
	require.Equal(t, peers, direct["participant_ids"])
	code, channel := call(t, http.MethodPost, api+"/rooms/channels", c.access, `{"name":"invited","private":true}`)
	require.Equal(t, 201, code, channel)
	require.NotContains(t, channel, "participant_ids")
	code, _ = call(t, http.MethodPut, api+"/rooms/"+channel["room_id"].(string)+"/members/"+ids[0], c.access, "")
	require.Equal(t, 204, code)
	code, message := call(t, http.MethodPost, api+"/rooms/"+direct["room_id"].(string)+"/messages", b.access, `{"client_message_id":"`+uuid.NewString()+`","text":"ping"}`)
	require.Equal(t, 200, code, message)
	code, page = call(t, http.MethodGet, api+"/rooms?limit=1", a.access, "")
	require.Equal(t, 200, code, page)
	first := page["rooms"].([]any)[0].(map[string]any)
	require.Equal(t, direct["room_id"], first["room_id"])
	require.Equal(t, peers, first["participant_ids"])
	require.Equal(t, message["created_at"], first["last_message_at"])
	cursor := page["next_cursor"].(string)
	require.NotEmpty(t, cursor)
	code, page = call(t, http.MethodGet, api+"/rooms?limit=1&cursor="+cursor, a.access, "")
	require.Equal(t, 200, code, page)
	second := page["rooms"].([]any)[0].(map[string]any)
	require.Equal(t, channel["room_id"], second["room_id"])
	require.NotContains(t, second, "last_message_at")
	require.NotContains(t, second, "participant_ids")
	require.Empty(t, page["next_cursor"])
	for token, want := range map[string][]any{b.access: {direct["room_id"]}, c.access: {channel["room_id"]}} {
		code, page = call(t, http.MethodGet, api+"/rooms", token, "")
		require.Equal(t, 200, code, page)
		var got []any
		for _, room := range page["rooms"].([]any) {
			got = append(got, room.(map[string]any)["room_id"])
		}
		require.Equal(t, want, got)
	}
	code, _ = call(t, http.MethodGet, api+"/rooms", "", "")
	require.Equal(t, 401, code)
	for _, query := range []string{"limit=0", "limit=101", "cursor=1", "cursor=" + cursor + "&cursor=" + cursor, "owner_id=" + ids[0]} {
		code, response := call(t, http.MethodGet, api+"/rooms?"+query, a.access, "")
		require.Equal(t, 400, code, query, response)
	}
	// A forged actor header is ignored; the list belongs to the token's identity.
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+c.access, "x-user-id", ids[0]))
	resp, err := chatv1.NewChatServiceClient(chatConn).ListRooms(ctx, &chatv1.ListRoomsRequest{})
	require.NoError(t, err)
	require.Len(t, resp.GetRooms(), 1)
	require.Equal(t, channel["room_id"], resp.GetRooms()[0].GetRoom().GetId())
}
