package handler_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/handler/mocks"
)

func newHandler(t *testing.T) (*handler.GRPCHandler, *mocks.MockRoomService, *mocks.MockIdentityClient) {
	rooms, auth := mocks.NewMockRoomService(t), mocks.NewMockIdentityClient(t)
	return handler.NewGRPCHandler(rooms, auth, slog.New(slog.NewJSONHandler(io.Discard, nil))), rooms, auth
}

func authorizedContext() context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer access", "x-user-id", uuid.NewString()))
}

func TestDirect_VerifiesActorAndResolvesPeer(t *testing.T) {
	h, rooms, auth := newHandler(t)
	actor, peer := uuid.NewString(), uuid.NewString()
	ctx := authorizedContext()
	auth.EXPECT().ValidateToken(ctx, &authv1.ValidateTokenRequest{Token: "access"}).Return(&authv1.ValidateTokenResponse{Valid: true, UserId: actor}, nil).Once()
	auth.EXPECT().GetUser(ctx, &authv1.GetUserRequest{AccessToken: "access", UserId: peer}).Return(&authv1.GetUserResponse{UserId: peer}, nil).Once()
	room := &domain.Room{ID: uuid.NewString(), Kind: domain.Direct, Private: true}
	rooms.EXPECT().GetOrCreateDirect(ctx, actor, peer).Return(room, nil).Once()
	resp, err := h.GetOrCreateDirect(ctx, &chatv1.GetOrCreateDirectRequest{PeerUserId: peer})
	require.NoError(t, err)
	require.Equal(t, room.ID, resp.GetRoom().GetId())
	require.Equal(t, chatv1.RoomKind_ROOM_KIND_DIRECT, resp.GetRoom().GetKind())
}

func TestEveryRPC_RejectsMissingIdentity(t *testing.T) {
	h, rooms, auth := newHandler(t)
	_, a := h.GetOrCreateDirect(context.Background(), nil)
	_, b := h.CreateChannel(context.Background(), nil)
	_, c := h.GetRoom(context.Background(), nil)
	_, d := h.AddMember(context.Background(), nil)
	for _, err := range []error{a, b, c, d} {
		require.Equal(t, codes.Unauthenticated, status.Code(err))
	}
	require.Empty(t, rooms.Calls)
	require.Empty(t, auth.Calls)
}

func TestDirect_UnknownPeerDoesNotCreateRoom(t *testing.T) {
	h, rooms, auth := newHandler(t)
	ctx := authorizedContext()
	actor, peer := uuid.NewString(), uuid.NewString()
	auth.EXPECT().ValidateToken(mock.Anything, mock.Anything).Return(&authv1.ValidateTokenResponse{Valid: true, UserId: actor}, nil).Once()
	auth.EXPECT().GetUser(ctx, &authv1.GetUserRequest{AccessToken: "access", UserId: peer}).Return(nil, status.Error(codes.NotFound, "user not found")).Once()
	_, err := h.GetOrCreateDirect(ctx, &chatv1.GetOrCreateDirectRequest{PeerUserId: peer})
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Empty(t, rooms.Calls)
}

func TestChannelOperations_UseVerifiedActorAndResolveMembers(t *testing.T) {
	h, rooms, auth := newHandler(t)
	ctx := authorizedContext()
	actor, member, roomID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth.EXPECT().ValidateToken(ctx, &authv1.ValidateTokenRequest{Token: "access"}).Return(&authv1.ValidateTokenResponse{Valid: true, UserId: actor}, nil).Times(3)
	room := &domain.Room{ID: roomID, Kind: domain.Channel, OwnerID: actor, Name: "general"}
	rooms.EXPECT().CreateChannel(ctx, actor, "general", true).Return(room, nil).Once()
	resp, err := h.CreateChannel(ctx, &chatv1.CreateChannelRequest{Name: "general", Private: true})
	require.NoError(t, err)
	require.Equal(t, actor, resp.GetRoom().GetOwnerId())
	rooms.EXPECT().GetRoom(ctx, actor, roomID).Return(nil, domain.ErrNotFound).Once()
	_, err = h.GetRoom(ctx, &chatv1.GetRoomRequest{RoomId: roomID})
	require.Equal(t, codes.NotFound, status.Code(err))
	auth.EXPECT().GetUser(ctx, &authv1.GetUserRequest{AccessToken: "access", UserId: member}).Return(&authv1.GetUserResponse{UserId: member}, nil).Once()
	rooms.EXPECT().AddMember(ctx, actor, roomID, member).Return(domain.ErrForbidden).Once()
	_, err = h.AddMember(ctx, &chatv1.AddMemberRequest{RoomId: roomID, UserId: member})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}
