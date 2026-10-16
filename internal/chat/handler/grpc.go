// Package handler exposes Chat operations with identity verification through Auth.
package handler

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/validator"
)

// RoomService is the room business logic consumed by the gRPC handler.
type RoomService interface {
	GetOrCreateDirect(context.Context, string, string) (*domain.Room, error)
	CreateChannel(context.Context, string, string, bool) (*domain.Room, error)
	GetRoom(context.Context, string, string) (*domain.Room, error)
	AddMember(context.Context, string, string, string) error
}

// MessageService is the message business logic consumed by the gRPC handler.
type MessageService interface {
	SendMessage(context.Context, string, string, string, string) (*domain.Message, error)
	ListMessages(context.Context, string, string, string, int) (*service.MessagePage, error)
}

// IdentityClient is the authenticated Auth directory consumed by Chat.
type IdentityClient interface {
	ValidateToken(context.Context, *authv1.ValidateTokenRequest, ...grpc.CallOption) (*authv1.ValidateTokenResponse, error)
	GetUser(context.Context, *authv1.GetUserRequest, ...grpc.CallOption) (*authv1.GetUserResponse, error)
}

// GRPCHandler derives the actor from a verified token, never a user ID header.
type GRPCHandler struct {
	chatv1.UnimplementedChatServiceServer
	rooms    RoomService
	messages MessageService
	auth     IdentityClient
	logger   *slog.Logger
}

// NewGRPCHandler binds Chat business logic and the Auth RPC client.
func NewGRPCHandler(rooms RoomService, messages MessageService, auth IdentityClient, logger *slog.Logger) *GRPCHandler {
	return &GRPCHandler{rooms: rooms, messages: messages, auth: auth, logger: logger}
}

func bearerCredential(ctx context.Context) (string, bool) {
	values := metadata.ValueFromIncomingContext(ctx, "authorization")
	if len(values) != 1 {
		return "", false
	}
	scheme, token, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	for i := 0; i < len(token); i++ {
		if token[i] <= ' ' || token[i] >= 0x7f {
			return "", false
		}
	}
	return token, true
}

func (h *GRPCHandler) identity(ctx context.Context) (string, string, error) {
	token, ok := bearerCredential(ctx)
	if !ok {
		return "", "", status.Error(codes.Unauthenticated, "authentication required")
	}
	resp, err := h.auth.ValidateToken(ctx, &authv1.ValidateTokenRequest{Token: token})
	if err != nil {
		return "", "", authStatus(err)
	}
	if !resp.GetValid() {
		return "", "", status.Error(codes.Unauthenticated, "invalid access token")
	}
	actor, err := validator.CanonicalID(resp.GetUserId())
	if err != nil {
		return "", "", status.Error(codes.Unauthenticated, "invalid identity")
	}
	return actor, token, nil
}

func (h *GRPCHandler) target(ctx context.Context, token, input string) (string, error) {
	id, err := validator.CanonicalID(input)
	if err != nil {
		return "", h.roomStatus(ctx, err)
	}
	user, err := h.auth.GetUser(ctx, &authv1.GetUserRequest{AccessToken: token, UserId: id})
	if err != nil {
		return "", authStatus(err)
	}
	if user.GetUserId() != id {
		return "", status.Error(codes.Internal, "invalid directory response")
	}
	return id, nil
}

// GetOrCreateDirect validates both the caller and the target identity.
func (h *GRPCHandler) GetOrCreateDirect(ctx context.Context, req *chatv1.GetOrCreateDirectRequest) (*chatv1.GetOrCreateDirectResponse, error) {
	actor, token, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	peer, err := h.target(ctx, token, req.GetPeerUserId())
	if err != nil {
		return nil, err
	}
	room, err := h.rooms.GetOrCreateDirect(ctx, actor, peer)
	if err != nil {
		return nil, h.roomStatus(ctx, err)
	}
	return &chatv1.GetOrCreateDirectResponse{Room: roomProto(room)}, nil
}

// CreateChannel makes the verified caller the owner of the new channel.
func (h *GRPCHandler) CreateChannel(ctx context.Context, req *chatv1.CreateChannelRequest) (*chatv1.CreateChannelResponse, error) {
	actor, _, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	room, err := h.rooms.CreateChannel(ctx, actor, req.GetName(), req.GetPrivate())
	if err != nil {
		return nil, h.roomStatus(ctx, err)
	}
	return &chatv1.CreateChannelResponse{Room: roomProto(room)}, nil
}

// GetRoom delegates membership filtering to Chat's storage-backed service.
func (h *GRPCHandler) GetRoom(ctx context.Context, req *chatv1.GetRoomRequest) (*chatv1.GetRoomResponse, error) {
	actor, _, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	room, err := h.rooms.GetRoom(ctx, actor, req.GetRoomId())
	if err != nil {
		return nil, h.roomStatus(ctx, err)
	}
	return &chatv1.GetRoomResponse{Room: roomProto(room)}, nil
}

// AddMember resolves the target before the atomic owner-authorized write.
func (h *GRPCHandler) AddMember(ctx context.Context, req *chatv1.AddMemberRequest) (*chatv1.AddMemberResponse, error) {
	actor, token, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	user, err := h.target(ctx, token, req.GetUserId())
	if err != nil {
		return nil, err
	}
	if err := h.rooms.AddMember(ctx, actor, req.GetRoomId(), user); err != nil {
		return nil, h.roomStatus(ctx, err)
	}
	return &chatv1.AddMemberResponse{}, nil
}

func roomProto(room *domain.Room) *chatv1.Room {
	kind := chatv1.RoomKind_ROOM_KIND_CHANNEL
	if room.Kind == domain.Direct {
		kind = chatv1.RoomKind_ROOM_KIND_DIRECT
	}
	return &chatv1.Room{Id: room.ID, Kind: kind, Name: room.Name, OwnerId: room.OwnerID, Private: room.Private,
		CreatedAt: timestamppb.New(room.CreatedAt), ParticipantIds: room.ParticipantIDs}
}

func (h *GRPCHandler) roomStatus(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, domain.ErrMessageConflict):
		return status.Error(codes.AlreadyExists, "message key already used with different text")
	case errors.Is(err, validator.ErrInvalidText):
		return status.Error(codes.InvalidArgument, "invalid message text")
	case errors.Is(err, validator.ErrInvalidCursor):
		return status.Error(codes.InvalidArgument, "invalid history cursor")
	case errors.Is(err, service.ErrInvalidLimit):
		return status.Error(codes.InvalidArgument, "history limit must be 1–100")
	case errors.Is(err, domain.ErrNotFound):
		return status.Error(codes.NotFound, "room not found")
	case errors.Is(err, domain.ErrForbidden):
		return status.Error(codes.PermissionDenied, "room operation forbidden")
	case errors.Is(err, validator.ErrInvalidID):
		return status.Error(codes.InvalidArgument, "invalid identifier")
	case errors.Is(err, validator.ErrInvalidName):
		return status.Error(codes.InvalidArgument, "invalid channel name")
	case errors.Is(err, service.ErrSelfDialog):
		return status.Error(codes.InvalidArgument, "direct participants must differ")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "request timed out")
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "request canceled")
	default:
		h.logger.ErrorContext(ctx, "chat operation failed", "error", err)
		return status.Error(codes.Internal, "internal error")
	}
}

func authStatus(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, "identity lookup timed out")
	}
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, "request canceled")
	}
	switch status.Code(err) {
	case codes.Unauthenticated:
		return status.Error(codes.Unauthenticated, "invalid access token")
	case codes.NotFound:
		return status.Error(codes.NotFound, "user not found")
	case codes.InvalidArgument:
		return status.Error(codes.InvalidArgument, "invalid user identifier")
	case codes.DeadlineExceeded:
		return status.Error(codes.DeadlineExceeded, "identity lookup timed out")
	case codes.Canceled:
		return status.Error(codes.Canceled, "request canceled")
	case codes.Unavailable:
		return status.Error(codes.Unavailable, "identity service unavailable")
	default:
		return status.Error(codes.Internal, "identity lookup failed")
	}
}
