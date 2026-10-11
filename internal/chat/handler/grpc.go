// Package handler exposes Chat operations with identity verification through Auth.
package handler

import (
	"context"
	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"google.golang.org/grpc"
	"log/slog"
)

// RoomService is the room business logic consumed by the gRPC handler.
type RoomService interface {
	GetOrCreateDirect(context.Context, string, string) (*domain.Room, error)
	CreateChannel(context.Context, string, string, bool) (*domain.Room, error)
	GetRoom(context.Context, string, string) (*domain.Room, error)
	AddMember(context.Context, string, string, string) error
}

// IdentityClient is the authenticated Auth directory consumed by Chat.
type IdentityClient interface {
	ValidateToken(context.Context, *authv1.ValidateTokenRequest, ...grpc.CallOption) (*authv1.ValidateTokenResponse, error)
	GetUser(context.Context, *authv1.GetUserRequest, ...grpc.CallOption) (*authv1.GetUserResponse, error)
}

// GRPCHandler derives the actor from a verified token, never a user ID header.
type GRPCHandler struct {
	chatv1.UnimplementedChatServiceServer
	rooms  RoomService
	auth   IdentityClient
	logger *slog.Logger
}

// NewGRPCHandler binds Chat business logic and the Auth RPC client.
func NewGRPCHandler(rooms RoomService, auth IdentityClient, logger *slog.Logger) *GRPCHandler {
	return &GRPCHandler{rooms: rooms, auth: auth, logger: logger}
}
