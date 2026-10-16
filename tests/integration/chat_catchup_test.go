package integration

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/repository"
)

func TestChatMessages_CatchUpAfterPositionOldestFirst(t *testing.T) {
	pool := newChatPool(t)
	repo := repository.NewRoomRepository(pool)
	ctx := context.Background()
	a, b, outsider := uuid.NewString(), uuid.NewString(), uuid.NewString()
	room, err := repo.GetOrCreateDirect(ctx, a, b)
	require.NoError(t, err)
	page, err := repo.ListMessagesAfter(ctx, room.ID, a, 0, 10)
	require.NoError(t, err)
	require.Empty(t, page)
	var sent []*domain.Message
	for i := range 4 {
		message, err := repo.SendMessage(ctx, room.ID, a, uuid.NewString(), fmt.Sprint("message ", i))
		require.NoError(t, err)
		require.Equal(t, int64(i+1), message.Sequence)
		sent = append(sent, message)
	}
	page, err = repo.ListMessagesAfter(ctx, room.ID, b, 1, 2)
	require.NoError(t, err)
	require.Equal(t, sent[1:3], page)
	page, err = repo.ListMessagesAfter(ctx, room.ID, b, 4, 2)
	require.NoError(t, err)
	require.Empty(t, page)
	_, err = repo.ListMessagesAfter(ctx, room.ID, outsider, 0, 10)
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = repo.ListMessagesAfter(ctx, uuid.NewString(), a, 0, 10)
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestChatMessageAPI_CatchUpReturnsTheGap(t *testing.T) {
	authConn, _ := startAuth(t)
	chatConn, _ := startChat(t, authConn)
	api := startGateway(t, authConn, 1000, chatConn).URL + "/api/v1"
	var ids []string
	var credentials []tokens
	for _, name := range []string{"writer", "reader", "outsider"} {
		code, body := call(t, http.MethodPost, api+"/auth/register", "", fmt.Sprintf(`{"email":"%s@example.com","username":"%s","password":"password1"}`, name, name))
		require.Equal(t, 201, code, body)
		ids = append(ids, body["user_id"].(string))
		credentials = append(credentials, login(t, api+"/auth", name+"@example.com"))
	}
	writer, reader, outsider := credentials[0], credentials[1], credentials[2]
	code, room := call(t, http.MethodPost, api+"/rooms/direct", writer.access, `{"peer_user_id":"`+ids[1]+`"}`)
	require.Equal(t, 200, code, room)
	endpoint := api + "/rooms/" + room["room_id"].(string) + "/messages"
	var sent []any
	for i := range 5 {
		code, message := call(t, http.MethodPost, endpoint, writer.access, fmt.Sprintf(`{"client_message_id":"%s","text":"m%d"}`, uuid.NewString(), i))
		require.Equal(t, 200, code, message)
		require.EqualValues(t, i+1, message["sequence"])
		sent = append(sent, message)
	}
	// The reader saw sequence 2 before disconnecting and pages forward from it.
	code, page := call(t, http.MethodGet, endpoint+"?after=2&limit=2", reader.access, "")
	require.Equal(t, 200, code, page)
	require.Equal(t, sent[2:4], page["messages"])
	require.Equal(t, "4", page["next_cursor"])
	code, page = call(t, http.MethodGet, endpoint+"?after=4&limit=2", reader.access, "")
	require.Equal(t, 200, code, page)
	require.Equal(t, sent[4:], page["messages"])
	require.Empty(t, page["next_cursor"])
	code, page = call(t, http.MethodGet, endpoint+"?after=0", reader.access, "")
	require.Equal(t, 200, code, page)
	require.Equal(t, sent, page["messages"])
	code, page = call(t, http.MethodGet, endpoint+"?limit=1", reader.access, "")
	require.Equal(t, 200, code, page)
	require.Equal(t, sent[4:], page["messages"])
	for _, query := range []string{"?after=02", "?after=1&cursor=3", "?after="} {
		code, _ = call(t, http.MethodGet, endpoint+query, reader.access, "")
		require.Equal(t, 400, code, query)
	}
	code, _ = call(t, http.MethodGet, endpoint+"?after=0", outsider.access, "")
	require.Equal(t, 404, code)
}
