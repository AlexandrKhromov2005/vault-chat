package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/response"
)

// ChatClient is the Chat RPC API consumed by the Gateway.
type ChatClient interface {
	SendMessage(context.Context, *chatv1.SendMessageRequest, ...grpc.CallOption) (*chatv1.SendMessageResponse, error)
	ListMessages(context.Context, *chatv1.ListMessagesRequest, ...grpc.CallOption) (*chatv1.ListMessagesResponse, error)
	GetOrCreateDirect(context.Context, *chatv1.GetOrCreateDirectRequest, ...grpc.CallOption) (*chatv1.GetOrCreateDirectResponse, error)
	CreateChannel(context.Context, *chatv1.CreateChannelRequest, ...grpc.CallOption) (*chatv1.CreateChannelResponse, error)
	GetRoom(context.Context, *chatv1.GetRoomRequest, ...grpc.CallOption) (*chatv1.GetRoomResponse, error)
	ListRooms(context.Context, *chatv1.ListRoomsRequest, ...grpc.CallOption) (*chatv1.ListRoomsResponse, error)
	AddMember(context.Context, *chatv1.AddMemberRequest, ...grpc.CallOption) (*chatv1.AddMemberResponse, error)
	ListMembers(context.Context, *chatv1.ListMembersRequest, ...grpc.CallOption) (*chatv1.ListMembersResponse, error)
}

// ChatHandler translates authenticated REST requests into Chat RPC calls.
type ChatHandler struct {
	client ChatClient
	logger *slog.Logger
}

// NewChatHandler binds the Chat RPC client and structured logger.
func NewChatHandler(client ChatClient, logger *slog.Logger) *ChatHandler {
	return &ChatHandler{client: client, logger: logger}
}

type roomResponse struct {
	ID        string    `json:"room_id"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name,omitempty"`
	OwnerID   string    `json:"owner_id,omitempty"`
	Private   bool      `json:"private"`
	CreatedAt time.Time `json:"created_at"`
	// ParticipantIDs is present only for direct dialogs.
	ParticipantIDs []string `json:"participant_ids,omitempty"`
}

func roomJSON(room *chatv1.Room) roomResponse {
	kind := "channel"
	if room.GetKind() == chatv1.RoomKind_ROOM_KIND_DIRECT {
		kind = "direct"
	}
	return roomResponse{ID: room.GetId(), Kind: kind, Name: room.GetName(), OwnerID: room.GetOwnerId(), Private: room.GetPrivate(),
		CreatedAt: room.GetCreatedAt().AsTime(), ParticipantIDs: room.GetParticipantIds()}
}

func chatContext(w http.ResponseWriter, r *http.Request) (context.Context, bool) {
	identity, ok := middleware.IdentityFromContext(r.Context())
	if !ok || identity.Token == "" {
		response.Error(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return nil, false
	}
	md, _ := metadata.FromOutgoingContext(r.Context())
	md = md.Copy()
	md.Set("authorization", "Bearer "+identity.Token)
	return metadata.NewOutgoingContext(r.Context(), md), true
}

// Direct obtains the caller's dialog with an existing user, idempotently.
func (h *ChatHandler) Direct(w http.ResponseWriter, r *http.Request) {
	ctx, ok := chatContext(w, r)
	if !ok {
		return
	}
	var req struct {
		PeerUserID string `json:"peer_user_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		err.write(w)
		return
	}
	resp, err := h.client.GetOrCreateDirect(ctx, &chatv1.GetOrCreateDirectRequest{PeerUserId: req.PeerUserID})
	if err != nil {
		h.backendError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, roomJSON(resp.GetRoom()))
}

// CreateChannel creates a channel owned by the caller.
func (h *ChatHandler) CreateChannel(w http.ResponseWriter, r *http.Request) {
	ctx, ok := chatContext(w, r)
	if !ok {
		return
	}
	var req struct {
		Name    string `json:"name"`
		Private bool   `json:"private"`
	}
	if err := decodeJSON(r, &req); err != nil {
		err.write(w)
		return
	}
	resp, err := h.client.CreateChannel(ctx, &chatv1.CreateChannelRequest{Name: req.Name, Private: req.Private})
	if err != nil {
		h.backendError(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, roomJSON(resp.GetRoom()))
}

// Room returns metadata visible only to the authenticated room member.
func (h *ChatHandler) Room(w http.ResponseWriter, r *http.Request) {
	ctx, ok := chatContext(w, r)
	if !ok {
		return
	}
	resp, err := h.client.GetRoom(ctx, &chatv1.GetRoomRequest{RoomId: r.PathValue("room_id")})
	if err != nil {
		h.backendError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, roomJSON(resp.GetRoom()))
}

// AddMember performs an idempotent, owner-authorized channel enrollment.
func (h *ChatHandler) AddMember(w http.ResponseWriter, r *http.Request) {
	ctx, ok := chatContext(w, r)
	if !ok {
		return
	}
	_, err := h.client.AddMember(ctx, &chatv1.AddMemberRequest{RoomId: r.PathValue("room_id"), UserId: r.PathValue("user_id")})
	if err != nil {
		h.backendError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *ChatHandler) backendError(w http.ResponseWriter, r *http.Request, err error) {
	code, tag, message := response.FromGRPC(err)
	if code >= 500 {
		h.logger.ErrorContext(r.Context(), "chat backend call failed", "error", err)
	}
	response.Error(w, code, tag, message)
}
