package handler

import (
	"net/http"
	"time"

	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/validator"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/response"
)

type roomSummaryResponse struct {
	roomResponse
	LastMessageAt *time.Time `json:"last_message_at,omitempty"`
}

// Rooms lists the caller's rooms, most recently active first, with a next-page cursor.
func (h *ChatHandler) Rooms(w http.ResponseWriter, r *http.Request) {
	ctx, ok := chatContext(w, r)
	if !ok {
		return
	}
	cursor, limit, err := roomsQuery(r.URL.RawQuery)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "invalid_argument", "invalid room list query")
		return
	}
	resp, err := h.client.ListRooms(ctx, &chatv1.ListRoomsRequest{Cursor: cursor, Limit: limit})
	if err != nil {
		h.backendError(w, r, err)
		return
	}
	page := struct {
		Rooms      []roomSummaryResponse `json:"rooms"`
		NextCursor string                `json:"next_cursor"`
	}{Rooms: make([]roomSummaryResponse, 0, len(resp.GetRooms())), NextCursor: resp.GetNextCursor()}
	for _, summary := range resp.GetRooms() {
		room := roomSummaryResponse{roomResponse: roomJSON(summary.GetRoom())}
		if summary.GetLastMessageAt() != nil {
			last := summary.GetLastMessageAt().AsTime()
			room.LastMessageAt = &last
		}
		page.Rooms = append(page.Rooms, room)
	}
	response.JSON(w, http.StatusOK, page)
}

func roomsQuery(raw string) (string, int32, error) {
	return pageQuery(raw, func(cursor string) error {
		_, err := validator.RoomCursor(cursor)
		return err
	})
}
