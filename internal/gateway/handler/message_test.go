package handler_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler/mocks"
)

func TestMessageREST_CredentialsAndResponses(t *testing.T) {
	client := mocks.NewMockChatClient(t)
	h := handler.NewChatHandler(client,slog.New(slog.NewJSONHandler(io.Discard,nil)))
	credential := mock.MatchedBy(func(ctx context.Context) bool { md,_ := metadata.FromOutgoingContext(ctx); return len(md.Get("authorization")) == 1 && md.Get("authorization")[0] == "Bearer access" })
	message := &chatv1.Message{Id:"message",RoomId:"room",SenderId:"actor",ClientMessageId:"key",Text:"hello"}
	client.EXPECT().SendMessage(credential,&chatv1.SendMessageRequest{RoomId:"room",ClientMessageId:"key",Text:"hello"}).Return(&chatv1.SendMessageResponse{Message:message},nil).Once()
	req := chatRequest(http.MethodPost,`{"client_message_id":"key","text":"hello"}`)
	req.SetPathValue("room_id","room")
	rec := httptest.NewRecorder()
	h.SendMessage(rec,req)
	require.Equal(t,200,rec.Code)
	require.Contains(t,rec.Body.String(),`"message_id":"message"`)
	req = chatRequest(http.MethodGet,"")
	req.SetPathValue("room_id","room")
	req.URL.RawQuery = "cursor=5&limit=2"
	client.EXPECT().ListMessages(credential,&chatv1.ListMessagesRequest{RoomId:"room",Cursor:"5",Limit:2}).Return(&chatv1.ListMessagesResponse{Messages:[]*chatv1.Message{message},NextCursor:"3"},nil).Once()
	rec = httptest.NewRecorder()
	h.Messages(rec,req)
	require.Equal(t,200,rec.Code)
	require.Contains(t,rec.Body.String(),`"next_cursor":"3"`)
	client.EXPECT().ListMessages(mock.Anything,&chatv1.ListMessagesRequest{RoomId:"room"}).Return(&chatv1.ListMessagesResponse{},nil).Once()
	req.URL.RawQuery = ""
	rec = httptest.NewRecorder()
	h.Messages(rec,req)
	require.Contains(t,rec.Body.String(),`"messages":[]`)
}

func TestMessageREST_RejectsAmbiguousInputs(t *testing.T) {
	client := mocks.NewMockChatClient(t)
	h := handler.NewChatHandler(client,slog.New(slog.NewJSONHandler(io.Discard,nil)))
	for _, method := range []http.HandlerFunc{h.SendMessage,h.Messages} {
		rec := httptest.NewRecorder()
		method(rec,httptest.NewRequest(http.MethodGet,"/",nil))
		require.Equal(t,401,rec.Code)
	}
	for _, body := range []string{`{"client_message_id":"key","text":"hi","sender_id":"other"}`,`{} {}`,`{`} {
		rec := httptest.NewRecorder()
		h.SendMessage(rec,chatRequest(http.MethodPost,body))
		require.Equal(t,400,rec.Code)
	}
	for _, query := range []string{"limit=101","limit=-1","limit=0","limit=01","limit=","limit=1&limit=2","cursor=1&cursor=2","cursor=%zz","actor=x","cursor=01"} {
		req := chatRequest(http.MethodGet,"")
		req.URL.RawQuery = query
		rec := httptest.NewRecorder()
		h.Messages(rec,req)
		require.Equal(t,400,rec.Code,query)
	}
	require.Empty(t,client.Calls)
}
