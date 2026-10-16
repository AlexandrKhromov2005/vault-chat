package handler

import (
	"net/http"
	"time"

	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/validator"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/response"
)

type memberResponse struct {
	UserID   string    `json:"user_id"`
	JoinedAt time.Time `json:"joined_at"`
}

// Members returns a member-only roster page ordered by user ID.
func (h *ChatHandler) Members(w http.ResponseWriter, r *http.Request) {
	ctx, ok := chatContext(w, r)
	if !ok {
		return
	}
	cursor, limit, err := pageQuery(r.URL.RawQuery, func(cursor string) error {
		_, err := validator.MemberCursor(cursor)
		return err
	})
	if err != nil {
		response.Error(w, http.StatusBadRequest, "invalid_argument", "invalid member list query")
		return
	}
	resp, err := h.client.ListMembers(ctx, &chatv1.ListMembersRequest{RoomId: r.PathValue("room_id"), Cursor: cursor, Limit: limit})
	if err != nil {
		h.backendError(w, r, err)
		return
	}
	page := struct {
		Members    []memberResponse `json:"members"`
		NextCursor string           `json:"next_cursor"`
	}{Members: make([]memberResponse, 0, len(resp.GetMembers())), NextCursor: resp.GetNextCursor()}
	for _, member := range resp.GetMembers() {
		page.Members = append(page.Members, memberResponse{UserID: member.GetUserId(), JoinedAt: member.GetJoinedAt().AsTime()})
	}
	response.JSON(w, http.StatusOK, page)
}

// RemoveMember performs an idempotent, owner-authorized channel removal.
func (h *ChatHandler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	ctx, ok := chatContext(w, r)
	if !ok {
		return
	}
	_, err := h.client.RemoveMember(ctx, &chatv1.RemoveMemberRequest{RoomId: r.PathValue("room_id"), UserId: r.PathValue("user_id")})
	if err != nil {
		h.backendError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// LeaveRoom removes the caller's own channel membership.
func (h *ChatHandler) LeaveRoom(w http.ResponseWriter, r *http.Request) {
	ctx, ok := chatContext(w, r)
	if !ok {
		return
	}
	if _, err := h.client.LeaveRoom(ctx, &chatv1.LeaveRoomRequest{RoomId: r.PathValue("room_id")}); err != nil {
		h.backendError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
