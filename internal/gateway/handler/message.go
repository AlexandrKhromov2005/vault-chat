package handler

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/validator"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/response"
)

type messageResponse struct {
	ID        string    `json:"message_id"`
	RoomID    string    `json:"room_id"`
	SenderID  string    `json:"sender_id"`
	ClientID  string    `json:"client_message_id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

func messageJSON(message *chatv1.Message) messageResponse {
	return messageResponse{ID: message.GetId(), RoomID: message.GetRoomId(), SenderID: message.GetSenderId(), ClientID: message.GetClientMessageId(), Text: message.GetText(), CreatedAt: message.GetCreatedAt().AsTime()}
}

// SendMessage returns 200 for both the initial send and an identical retry.
func (h *ChatHandler) SendMessage(w http.ResponseWriter, r *http.Request) {
	ctx, ok := chatContext(w, r)
	if !ok {
		return
	}
	var req struct {
		ClientID string `json:"client_message_id"`
		Text     string `json:"text"`
	}
	if err := decodeJSON(r, &req); err != nil {
		err.write(w)
		return
	}
	resp, err := h.client.SendMessage(ctx, &chatv1.SendMessageRequest{RoomId: r.PathValue("room_id"), ClientMessageId: req.ClientID, Text: req.Text})
	if err != nil {
		h.backendError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, messageJSON(resp.GetMessage()))
}

// Messages returns member-only newest-first history with an older-page cursor.
func (h *ChatHandler) Messages(w http.ResponseWriter, r *http.Request) {
	ctx, ok := chatContext(w, r)
	if !ok {
		return
	}
	cursor, limit, err := historyQuery(r.URL.RawQuery)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "invalid_argument", "invalid history query")
		return
	}
	resp, err := h.client.ListMessages(ctx, &chatv1.ListMessagesRequest{RoomId: r.PathValue("room_id"), Cursor: cursor, Limit: limit})
	if err != nil {
		h.backendError(w, r, err)
		return
	}
	page := struct {
		Messages   []messageResponse `json:"messages"`
		NextCursor string            `json:"next_cursor"`
	}{Messages: make([]messageResponse, 0, len(resp.GetMessages())), NextCursor: resp.GetNextCursor()}
	for _, message := range resp.GetMessages() {
		page.Messages = append(page.Messages, messageJSON(message))
	}
	response.JSON(w, http.StatusOK, page)
}

func historyQuery(raw string) (string, int32, error) {
	return pageQuery(raw, func(cursor string) error {
		_, err := validator.HistoryCursor(cursor)
		return err
	})
}

// pageQuery accepts at most one cursor and one canonical limit of 1–100;
// an absent limit is returned as zero so that the backend applies its default.
func pageQuery(raw string, validCursor func(string) error) (string, int32, error) {
	query, err := url.ParseQuery(raw)
	if err != nil {
		return "", 0, err
	}
	for key, values := range query {
		if (key != "cursor" && key != "limit") || len(values) != 1 {
			return "", 0, fmt.Errorf("unexpected page parameter")
		}
	}
	cursor := query.Get("cursor")
	if err := validCursor(cursor); err != nil {
		return "", 0, err
	}
	var limit int64
	if value, exists := query["limit"]; exists {
		limit, err = strconv.ParseInt(value[0], 10, 32)
		if err != nil || limit < 1 || limit > 100 || strconv.FormatInt(limit, 10) != value[0] {
			return "", 0, fmt.Errorf("invalid page limit")
		}
	}
	return cursor, int32(limit), nil
}
