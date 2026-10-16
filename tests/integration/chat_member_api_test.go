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

func TestChatMemberAPI_RosterRemovalAndLeaving(t *testing.T) {
	authConn, _ := startAuth(t)
	chatConn, _ := startChat(t, authConn)
	gateway := startGateway(t, authConn, 1000, chatConn)
	api := gateway.URL + "/api/v1"
	var ids []string
	var credentials []tokens
	for _, name := range []string{"owner", "member", "leaver", "outsider"} {
		code, body := call(t, http.MethodPost, api+"/auth/register", "", fmt.Sprintf(`{"email":"%s@example.com","username":"%s","password":"password1"}`, name, name))
		require.Equal(t, 201, code, body)
		ids = append(ids, body["user_id"].(string))
		credentials = append(credentials, login(t, api+"/auth", name+"@example.com"))
	}
	owner, member, leaver, outsider := credentials[0], credentials[1], credentials[2], credentials[3]
	code, channel := call(t, http.MethodPost, api+"/rooms/channels", owner.access, `{"name":"members","private":true}`)
	require.Equal(t, 201, code, channel)
	room := api + "/rooms/" + channel["room_id"].(string)
	for _, id := range ids[1:3] {
		code, _ = call(t, http.MethodPut, room+"/members/"+id, owner.access, "")
		require.Equal(t, 204, code)
	}
	code, message := call(t, http.MethodPost, room+"/messages", member.access, `{"client_message_id":"`+uuid.NewString()+`","text":"hi"}`)
	require.Equal(t, 200, code, message)
	code, roster := call(t, http.MethodGet, room+"/members?limit=2", member.access, "")
	require.Equal(t, 200, code, roster)
	require.Len(t, roster["members"], 2)
	code, rest := call(t, http.MethodGet, room+"/members?cursor="+roster["next_cursor"].(string), member.access, "")
	require.Equal(t, 200, code, rest)
	require.Len(t, rest["members"], 1)
	require.Empty(t, rest["next_cursor"])
	code, hidden := call(t, http.MethodGet, room+"/members", outsider.access, "")
	require.Equal(t, 404, code, hidden)
	code, absent := call(t, http.MethodGet, api+"/rooms/"+uuid.NewString()+"/members", outsider.access, "")
	require.Equal(t, 404, code)
	require.Equal(t, hidden, absent)
	code, _ = call(t, http.MethodDelete, room+"/members/"+ids[2], member.access, "")
	require.Equal(t, 403, code)
	code, conflict := call(t, http.MethodDelete, room+"/members/"+ids[0], owner.access, "")
	require.Equal(t, 409, code, conflict)
	require.Equal(t, "failed_precondition", conflict["error"].(map[string]any)["code"])
	code, conflict = call(t, http.MethodDelete, room+"/members/me", owner.access, "")
	require.Equal(t, 409, code, conflict)
	// Removal takes effect on the very next request, including history.
	for range 2 {
		code, _ = call(t, http.MethodDelete, room+"/members/"+ids[1], owner.access, "")
		require.Equal(t, 204, code)
	}
	for _, path := range []string{"", "/messages", "/members"} {
		code, _ = call(t, http.MethodGet, room+path, member.access, "")
		require.Equal(t, 404, code, path)
	}
	code, _ = call(t, http.MethodPost, room+"/messages", member.access, `{"client_message_id":"`+uuid.NewString()+`","text":"still here?"}`)
	require.Equal(t, 404, code)
	code, list := call(t, http.MethodGet, api+"/rooms", member.access, "")
	require.Equal(t, 200, code, list)
	require.Empty(t, list["rooms"])
	code, _ = call(t, http.MethodDelete, room+"/members/me", leaver.access, "")
	require.Equal(t, 204, code)
	code, _ = call(t, http.MethodGet, room+"/messages", leaver.access, "")
	require.Equal(t, 404, code)
	code, _ = call(t, http.MethodDelete, room+"/members/me", leaver.access, "")
	require.Equal(t, 404, code)
	code, roster = call(t, http.MethodGet, room+"/members", owner.access, "")
	require.Equal(t, 200, code, roster)
	require.Len(t, roster["members"], 1)
	require.Equal(t, ids[0], roster["members"].([]any)[0].(map[string]any)["user_id"])
	code, direct := call(t, http.MethodPost, api+"/rooms/direct", owner.access, `{"peer_user_id":"`+ids[3]+`"}`)
	require.Equal(t, 200, code, direct)
	code, _ = call(t, http.MethodDelete, api+"/rooms/"+direct["room_id"].(string)+"/members/me", outsider.access, "")
	require.Equal(t, 403, code)
	code, roster = call(t, http.MethodGet, api+"/rooms/"+direct["room_id"].(string)+"/members", outsider.access, "")
	require.Equal(t, 200, code, roster)
	require.Len(t, roster["members"], 2)
	// Removal through Chat directly still derives the owner from the token.
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+outsider.access, "x-user-id", ids[0]))
	_, err := chatv1.NewChatServiceClient(chatConn).RemoveMember(ctx, &chatv1.RemoveMemberRequest{RoomId: channel["room_id"].(string), UserId: ids[0]})
	require.Equal(t, codes.NotFound, status.Code(err))
}
