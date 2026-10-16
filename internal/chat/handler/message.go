package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"google.golang.org/protobuf/types/known/timestamppb"

	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
)

// SendMessage derives the sender from Auth before storage checks membership.
func (h *GRPCHandler) SendMessage(ctx context.Context, req *chatv1.SendMessageRequest) (*chatv1.SendMessageResponse, error) {
	actor, token, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	var message *domain.Message
	if len(req.GetAttachmentIds()) > 0 {
		sender, ok := h.messages.(interface {
			SendAttachments(context.Context, string, string, string, string, []string) (*domain.Message, error)
		})
		if !ok {
			return nil, status.Error(codes.Unavailable, "media unavailable")
		}
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		message, err = sender.SendAttachments(ctx, actor, req.GetRoomId(), req.GetClientMessageId(), req.GetText(), req.GetAttachmentIds())
	} else {
		message, err = h.messages.SendMessage(ctx, actor, req.GetRoomId(), req.GetClientMessageId(), req.GetText())
	}
	if err != nil {
		return nil, h.roomStatus(ctx, err)
	}
	return &chatv1.SendMessageResponse{Message: messageProto(message)}, nil
}

// ListMessages authenticates every page, including empty pages. A catch-up
// position selects oldest-first messages after it instead of older history.
func (h *GRPCHandler) ListMessages(ctx context.Context, req *chatv1.ListMessagesRequest) (*chatv1.ListMessagesResponse, error) {
	actor, _, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	page, err := h.messages.History(ctx, actor, req.GetRoomId(), req.GetCursor(), req.GetAfter(), int(req.GetLimit()))
	if err != nil {
		return nil, h.roomStatus(ctx, err)
	}
	response := &chatv1.ListMessagesResponse{Messages: make([]*chatv1.Message, 0, len(page.Messages)), NextCursor: page.NextCursor}
	for _, message := range page.Messages {
		response.Messages = append(response.Messages, messageProto(message))
	}
	return response, nil
}

func messageProto(message *domain.Message) *chatv1.Message {
	return &chatv1.Message{Id: message.ID, RoomId: message.RoomID, SenderId: message.SenderID, ClientMessageId: message.ClientID, Text: message.Text, CreatedAt: timestamppb.New(message.CreatedAt), Sequence: message.Sequence, AttachmentIds: message.AttachmentIDs}
}
