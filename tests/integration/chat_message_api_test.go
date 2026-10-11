package integration

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
)

func TestChatMessageAPI_ExchangeIsolationAndHistory(t *testing.T) {
	authConn, _ := startAuth(t)
	chatConn, _ := startChat(t, authConn)
	gateway := startGateway(t, authConn, 1000, chatConn)
	api := gateway.URL + "/api/v1"
	var ids []string
	var credentials []tokens
	for _, name := range []string{"sender", "recipient", "outsider"} {
		code, body := call(t, http.MethodPost, api+"/auth/register", "", fmt.Sprintf(`{"email":"%s@example.com","username":"%s","password":"password1"}`, name, name))
		require.Equal(t, 201, code, body)
		ids = append(ids, body["user_id"].(string))
		credentials = append(credentials, login(t, api+"/auth", name+"@example.com"))
	}
	a, b, c := credentials[0], credentials[1], credentials[2]
	code, room := call(t, http.MethodPost, api+"/rooms/direct", a.access, `{"peer_user_id":"`+ids[1]+`"}`)
	require.Equal(t, 200, code, room)
	roomID := room["room_id"].(string)
	endpoint := api + "/rooms/" + roomID + "/messages"
	code, page := call(t, http.MethodGet, endpoint, a.access, "")
	require.Equal(t, 200, code, page)
	require.Empty(t, page["messages"])
	require.Empty(t, page["next_cursor"])
	key := uuid.NewString()
	body := `{"client_message_id":"` + key + `","text":"Привет\nмир"}`
	code, first := call(t, http.MethodPost, endpoint, a.access, body)
	require.Equal(t, 200, code, first)
	require.Equal(t, ids[0], first["sender_id"])
	code, retry := call(t, http.MethodPost, endpoint, a.access, body)
	require.Equal(t, 200, code, retry)
	require.Equal(t, first, retry)
	code, conflict := call(t, http.MethodPost, endpoint, a.access, `{"client_message_id":"`+key+`","text":"changed"}`)
	require.Equal(t, 409, code, conflict)
	code, second := call(t, http.MethodPost, endpoint, b.access, `{"client_message_id":"`+key+`","text":"reply"}`)
	require.Equal(t, 200, code, second)
	for _, token := range []string{"", a.refresh} {
		code, response := call(t, http.MethodPost, endpoint, token, body)
		require.Equal(t, 401, code, response)
		code, response = call(t, http.MethodGet, endpoint, token, "")
		require.Equal(t, 401, code, response)
	}
	code, denied := call(t, http.MethodPost, endpoint, c.access, body)
	require.Equal(t, 404, code, denied)
	code, denied = call(t, http.MethodGet, endpoint, c.access, "")
	require.Equal(t, 404, code, denied)
	code, absent := call(t, http.MethodGet, api+"/rooms/"+uuid.NewString()+"/messages", c.access, "")
	require.Equal(t, 404, code, absent)
	require.Equal(t, denied, absent)
	code, page = call(t, http.MethodGet, endpoint+"?limit=1", a.access, "")
	require.Equal(t, 200, code, page)
	require.Equal(t, second, page["messages"].([]any)[0])
	cursor := page["next_cursor"].(string)
	require.NotEmpty(t, cursor)
	// New sends cannot change which older records the issued cursor selects.
	code, newest := call(t, http.MethodPost, endpoint, a.access, `{"client_message_id":"`+uuid.NewString()+`","text":"new"}`)
	require.Equal(t, 200, code, newest)
	code, page = call(t, http.MethodGet, endpoint+"?limit=1&cursor="+cursor, b.access, "")
	require.Equal(t, 200, code, page)
	require.Equal(t, first, page["messages"].([]any)[0])
	require.Empty(t, page["next_cursor"])
	for _, invalid := range []string{`{"client_message_id":"` + uuid.NewString() + `","text":" "}`, `{"client_message_id":"bad","text":"ok"}`, `{"client_message_id":"` + uuid.NewString() + `","text":"ok","sender_id":"` + ids[1] + `"}`} {
		code, response := call(t, http.MethodPost, endpoint, a.access, invalid)
		require.Equal(t, 400, code, response)
	}
	client := chatv1.NewChatServiceClient(chatConn)
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+c.access, "x-user-id", ids[0]))
	_, err := client.SendMessage(ctx, &chatv1.SendMessageRequest{RoomId: roomID, ClientMessageId: key, Text: "spoof"})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = client.ListMessages(ctx, &chatv1.ListMessagesRequest{RoomId: roomID})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = client.ListMessages(context.Background(), &chatv1.ListMessagesRequest{RoomId: roomID})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	// Channels use the same membership boundary, including after invitation.
	code, channel := call(t, http.MethodPost, api+"/rooms/channels", a.access, `{"name":"general","private":false}`)
	require.Equal(t, 201, code, channel)
	channelURL := api + "/rooms/" + channel["room_id"].(string)
	code, response := call(t, http.MethodPost, channelURL+"/messages", b.access, body)
	require.Equal(t, 404, code, response)
	code, response = call(t, http.MethodPut, channelURL+"/members/"+ids[1], a.access, "")
	require.Equal(t, 204, code, response)
	code, response = call(t, http.MethodPost, channelURL+"/messages", b.access, body)
	require.Equal(t, 200, code, response)
	code, page = call(t, http.MethodGet, channelURL+"/messages", a.access, "")
	require.Equal(t, 200, code, page)
	require.Len(t, page["messages"], 1)
}
