package integration

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestChatMemberAPI_RosterOnlyForMembers(t *testing.T) {
	authConn, _ := startAuth(t)
	chatConn, _ := startChat(t, authConn)
	gateway := startGateway(t, authConn, 1000, chatConn)
	api := gateway.URL + "/api/v1"
	var ids []string
	var credentials []tokens
	for _, name := range []string{"owner", "member", "invitee", "outsider"} {
		code, body := call(t, http.MethodPost, api+"/auth/register", "", fmt.Sprintf(`{"email":"%s@example.com","username":"%s","password":"password1"}`, name, name))
		require.Equal(t, 201, code, body)
		ids = append(ids, body["user_id"].(string))
		credentials = append(credentials, login(t, api+"/auth", name+"@example.com"))
	}
	owner, member, outsider := credentials[0], credentials[1], credentials[3]
	code, channel := call(t, http.MethodPost, api+"/rooms/channels", owner.access, `{"name":"members","private":true}`)
	require.Equal(t, 201, code, channel)
	room := api + "/rooms/" + channel["room_id"].(string)
	for _, id := range ids[1:3] {
		code, _ = call(t, http.MethodPut, room+"/members/"+id, owner.access, "")
		require.Equal(t, 204, code)
	}
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
	code, direct := call(t, http.MethodPost, api+"/rooms/direct", owner.access, `{"peer_user_id":"`+ids[3]+`"}`)
	require.Equal(t, 200, code, direct)
	code, roster = call(t, http.MethodGet, api+"/rooms/"+direct["room_id"].(string)+"/members", outsider.access, "")
	require.Equal(t, 200, code, roster)
	require.Len(t, roster["members"], 2)
}
