package handler_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/validator"
)

func TestMemberRPCs_UseVerifiedActorWithoutDirectoryLookups(t *testing.T) {
	h, rooms, auth := newHandler(t)
	ctx, actor, roomID, member := authorizedContext(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth.EXPECT().ValidateToken(ctx, &authv1.ValidateTokenRequest{Token: "access"}).Return(&authv1.ValidateTokenResponse{Valid: true, UserId: actor}, nil).Times(3)
	joined := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	rooms.EXPECT().ListMembers(ctx, actor, roomID, "cursor", 5).Return(&service.MemberPage{Members: []*domain.Member{{UserID: member, JoinedAt: joined}}, NextCursor: member}, nil).Once()
	page, err := h.ListMembers(ctx, &chatv1.ListMembersRequest{RoomId: roomID, Cursor: "cursor", Limit: 5})
	require.NoError(t, err)
	require.Equal(t, member, page.GetNextCursor())
	require.Equal(t, member, page.GetMembers()[0].GetUserId())
	require.True(t, joined.Equal(page.GetMembers()[0].GetJoinedAt().AsTime()))
	rooms.EXPECT().RemoveMember(ctx, actor, roomID, member).Return(nil).Once()
	_, err = h.RemoveMember(ctx, &chatv1.RemoveMemberRequest{RoomId: roomID, UserId: member})
	require.NoError(t, err)
	rooms.EXPECT().LeaveRoom(ctx, actor, roomID).Return(nil).Once()
	_, err = h.LeaveRoom(ctx, &chatv1.LeaveRoomRequest{RoomId: roomID})
	require.NoError(t, err)
	_, a := h.ListMembers(context.Background(), nil)
	_, b := h.RemoveMember(context.Background(), nil)
	_, c := h.LeaveRoom(context.Background(), nil)
	for _, err := range []error{a, b, c} {
		require.Equal(t, codes.Unauthenticated, status.Code(err))
	}
}

func TestMemberRPCs_ClassifyMembershipErrors(t *testing.T) {
	for _, tc := range []struct {
		err     error
		code    codes.Code
		message string
	}{
		{domain.ErrOwnerMembership, codes.FailedPrecondition, "channel owner cannot leave or be removed"},
		{domain.ErrForbidden, codes.PermissionDenied, "room operation forbidden"},
		{domain.ErrNotFound, codes.NotFound, "room not found"},
		{validator.ErrInvalidMemberCursor, codes.InvalidArgument, "invalid member list cursor"},
		{service.ErrInvalidMemberLimit, codes.InvalidArgument, "member list limit must be 1–100"},
	} {
		t.Run(tc.message, func(t *testing.T) {
			h, rooms, auth := newHandler(t)
			ctx, actor := authorizedContext(), uuid.NewString()
			auth.EXPECT().ValidateToken(ctx, mock.Anything).Return(&authv1.ValidateTokenResponse{Valid: true, UserId: actor}, nil).Times(3)
			rooms.EXPECT().ListMembers(ctx, actor, "room", "", 0).Return(nil, tc.err).Once()
			rooms.EXPECT().RemoveMember(ctx, actor, "room", "user").Return(tc.err).Once()
			rooms.EXPECT().LeaveRoom(ctx, actor, "room").Return(tc.err).Once()
			_, a := h.ListMembers(ctx, &chatv1.ListMembersRequest{RoomId: "room"})
			_, b := h.RemoveMember(ctx, &chatv1.RemoveMemberRequest{RoomId: "room", UserId: "user"})
			_, c := h.LeaveRoom(ctx, &chatv1.LeaveRoomRequest{RoomId: "room"})
			for _, err := range []error{a, b, c} {
				require.Equal(t, tc.code, status.Code(err))
				require.Equal(t, tc.message, status.Convert(err).Message())
			}
		})
	}
}
