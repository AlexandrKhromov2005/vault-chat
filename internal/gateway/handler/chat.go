package handler

import (
	"context"
	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"google.golang.org/grpc"
	"log/slog"
)

// ChatClient is the room RPC API consumed by the Gateway.
type ChatClient interface {
	GetOrCreateDirect(context.Context, *chatv1.GetOrCreateDirectRequest, ...grpc.CallOption) (*chatv1.GetOrCreateDirectResponse, error)
	CreateChannel(context.Context, *chatv1.CreateChannelRequest, ...grpc.CallOption) (*chatv1.CreateChannelResponse, error)
	GetRoom(context.Context, *chatv1.GetRoomRequest, ...grpc.CallOption) (*chatv1.GetRoomResponse, error)
	AddMember(context.Context, *chatv1.AddMemberRequest, ...grpc.CallOption) (*chatv1.AddMemberResponse, error)
}

// ChatHandler translates authenticated REST requests into Chat RPC calls.
type ChatHandler struct {
	client ChatClient
	logger *slog.Logger
}

// NewChatHandler binds the Chat RPC client and structured logger.
func NewChatHandler(client ChatClient, logger *slog.Logger) *ChatHandler {
	return &ChatHandler{client: client, logger: logger}
}
