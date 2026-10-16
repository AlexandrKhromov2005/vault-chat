package handler

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
)

// ListRooms lists only rooms of the actor verified through Auth.
func (h *GRPCHandler) ListRooms(ctx context.Context, req *chatv1.ListRoomsRequest) (*chatv1.ListRoomsResponse, error) {
	actor, _, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	page, err := h.rooms.ListRooms(ctx, actor, req.GetCursor(), int(req.GetLimit()))
	if err != nil {
		return nil, h.roomStatus(ctx, err)
	}
	response := &chatv1.ListRoomsResponse{Rooms: make([]*chatv1.RoomSummary, 0, len(page.Rooms)), NextCursor: page.NextCursor}
	for _, room := range page.Rooms {
		summary := &chatv1.RoomSummary{Room: roomProto(&room.Room)}
		if room.LastMessageAt != nil {
			summary.LastMessageAt = timestamppb.New(*room.LastMessageAt)
		}
		response.Rooms = append(response.Rooms, summary)
	}
	return response, nil
}
