package handler

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
)

// ListMembers returns the roster only to a member verified through Auth.
func (h *GRPCHandler) ListMembers(ctx context.Context, req *chatv1.ListMembersRequest) (*chatv1.ListMembersResponse, error) {
	actor, _, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	page, err := h.rooms.ListMembers(ctx, actor, req.GetRoomId(), req.GetCursor(), int(req.GetLimit()))
	if err != nil {
		return nil, h.roomStatus(ctx, err)
	}
	response := &chatv1.ListMembersResponse{Members: make([]*chatv1.Member, 0, len(page.Members)), NextCursor: page.NextCursor}
	for _, member := range page.Members {
		response.Members = append(response.Members, &chatv1.Member{UserId: member.UserID, JoinedAt: timestamppb.New(member.JoinedAt)})
	}
	return response, nil
}

// RemoveMember delegates the owner check to the atomic storage write. Unlike
// AddMember it does not resolve the target, which may no longer exist.
func (h *GRPCHandler) RemoveMember(ctx context.Context, req *chatv1.RemoveMemberRequest) (*chatv1.RemoveMemberResponse, error) {
	actor, _, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.rooms.RemoveMember(ctx, actor, req.GetRoomId(), req.GetUserId()); err != nil {
		return nil, h.roomStatus(ctx, err)
	}
	return &chatv1.RemoveMemberResponse{}, nil
}

// LeaveRoom removes the verified caller's own channel membership.
func (h *GRPCHandler) LeaveRoom(ctx context.Context, req *chatv1.LeaveRoomRequest) (*chatv1.LeaveRoomResponse, error) {
	actor, _, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.rooms.LeaveRoom(ctx, actor, req.GetRoomId()); err != nil {
		return nil, h.roomStatus(ctx, err)
	}
	return &chatv1.LeaveRoomResponse{}, nil
}
