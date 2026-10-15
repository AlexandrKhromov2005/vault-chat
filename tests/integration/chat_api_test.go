package integration

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	chathandler "github.com/AlexandrKhromov2005/vault-chat/internal/chat/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/requestid"
)

// startChat serves Chat over an in-memory connection; options configure its
// repository, e.g. to publish events.
func startChat(t *testing.T, authConn *grpc.ClientConn, options ...repository.Option) (*grpc.ClientConn, func() []string) {
	t.Helper()
	pool := newChatPool(t)
	listener := bufconn.Listen(1 << 20)
	var ids []string
	var mu sync.Mutex
	server := grpc.NewServer(grpc.ChainUnaryInterceptor(requestid.UnaryServerInterceptor(), chathandler.UnaryTimeout(5*time.Second), func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		mu.Lock()
		ids = append(ids, requestid.FromContext(ctx))
		mu.Unlock()
		return next(ctx, req)
	}))
	rooms := repository.NewRoomRepository(pool, options...)
	h := chathandler.NewGRPCHandler(service.NewService(rooms), service.NewMessageService(rooms), authv1.NewAuthServiceClient(authConn), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	chatv1.RegisterChatServiceServer(server, h)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn := newAuthConn(t, func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) })
	return conn, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), ids...) }
}

func TestChatAPI_IdentityMembershipAndDirectory(t *testing.T) {
	authConn, authIDs := startAuth(t)
	chatConn, chatIDs := startChat(t, authConn)
	gateway := startGateway(t, authConn, 1000, chatConn)
	api := gateway.URL + "/api/v1"
	var ids []string
	var credentials []tokens
	for _, name := range []string{"alice", "bob", "charlie"} {
		code, body := call(t, http.MethodPost, api+"/auth/register", "", `{"email":"`+name+`@example.com","username":"`+name+`","password":"password1"}`)
		require.Equal(t, 201, code, body)
		ids = append(ids, body["user_id"].(string))
		credentials = append(credentials, login(t, api+"/auth", name+"@example.com"))
	}
	a, b, c := credentials[0], credentials[1], credentials[2]
	code, profile := call(t, http.MethodGet, api+"/users/"+ids[1], a.access, "")
	require.Equal(t, 200, code, profile)
	require.Equal(t, "bob", profile["username"])
	require.NotContains(t, profile, "email")
	require.NotContains(t, profile, "password_hash")
	code, _ = call(t, http.MethodGet, api+"/users/"+ids[1], "", "")
	require.Equal(t, 401, code)
	code, _ = call(t, http.MethodGet, api+"/users/"+ids[1], a.refresh, "")
	require.Equal(t, 401, code)
	code, _ = call(t, http.MethodPost, api+"/rooms/direct", a.access, `{"peer_user_id":"`+uuid.NewString()+`"}`)
	require.Equal(t, 404, code)
	code, _ = call(t, http.MethodPost, api+"/rooms/direct", a.access, `{"peer_user_id":"`+strings.ToUpper(ids[0])+`"}`)
	require.Equal(t, 400, code)
	code, direct := call(t, http.MethodPost, api+"/rooms/direct", a.access, `{"peer_user_id":"urn:uuid:`+ids[1]+`"}`)
	require.Equal(t, 200, code, direct)
	roomID := direct["room_id"].(string)
	code, reversed := call(t, http.MethodPost, api+"/rooms/direct", b.access, `{"peer_user_id":"`+ids[0]+`"}`)
	require.Equal(t, 200, code)
	require.Equal(t, roomID, reversed["room_id"])
	for _, token := range []string{a.access, b.access} {
		code, _ = call(t, http.MethodGet, api+"/rooms/"+roomID, token, "")
		require.Equal(t, 200, code)
	}
	code, hidden := call(t, http.MethodGet, api+"/rooms/"+roomID, c.access, "")
	require.Equal(t, 404, code)
	code, absent := call(t, http.MethodGet, api+"/rooms/"+uuid.NewString(), c.access, "")
	require.Equal(t, 404, code)
	require.Equal(t, hidden, absent)
	code, _ = call(t, http.MethodPut, api+"/rooms/"+roomID+"/members/"+ids[2], a.access, "")
	require.Equal(t, 403, code)
	code, channel := call(t, http.MethodPost, api+"/rooms/channels", a.access, `{"name":"Команда","private":false}`)
	require.Equal(t, 201, code, channel)
	require.Equal(t, ids[0], channel["owner_id"])
	channelID := channel["room_id"].(string)
	for i := 0; i < 2; i++ {
		code, _ = call(t, http.MethodPut, api+"/rooms/"+channelID+"/members/"+ids[1], a.access, "")
		require.Equal(t, 204, code)
	}
	code, _ = call(t, http.MethodGet, api+"/rooms/"+channelID, b.access, "")
	require.Equal(t, 200, code)
	code, _ = call(t, http.MethodPut, api+"/rooms/"+channelID+"/members/"+ids[2], b.access, "")
	require.Equal(t, 403, code)
	code, _ = call(t, http.MethodPut, api+"/rooms/"+channelID+"/members/"+uuid.NewString(), a.access, "")
	require.Equal(t, 404, code)
	code, _ = call(t, http.MethodGet, api+"/rooms/"+channelID, c.access, "")
	require.Equal(t, 404, code)
	// Calling Chat directly must still validate the credential and ignore a forged actor header.
	client := chatv1.NewChatServiceClient(chatConn)
	for _, token := range []string{"invalid", a.refresh} {
		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token, "x-user-id", ids[0]))
		_, err := client.GetRoom(ctx, &chatv1.GetRoomRequest{RoomId: roomID})
		require.Equal(t, codes.Unauthenticated, status.Code(err))
	}
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+c.access, "x-user-id", ids[0]))
	_, err := client.GetRoom(ctx, &chatv1.GetRoomRequest{RoomId: roomID})
	require.Equal(t, codes.NotFound, status.Code(err))
	req, err := http.NewRequest(http.MethodGet, api+"/rooms/"+roomID, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+a.access)
	req.Header.Set(requestid.Header, "chat-flow-1")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	require.Contains(t, chatIDs(), "chat-flow-1")
	require.Contains(t, authIDs(), "chat-flow-1")
	// An unavailable identity dependency must fail closed even when bypassing Gateway.
	require.NoError(t, authConn.Close())
	ctx = metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+a.access))
	_, err = client.CreateChannel(ctx, &chatv1.CreateChannelRequest{Name: "must not exist"})
	require.Contains(t, []codes.Code{codes.Unavailable, codes.Canceled}, status.Code(err))
}
