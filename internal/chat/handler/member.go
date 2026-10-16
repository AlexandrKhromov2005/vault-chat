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
