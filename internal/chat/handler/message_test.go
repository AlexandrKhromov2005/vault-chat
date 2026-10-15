package handler_test

import (
	"context"
	"io"
	"log/slog"
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
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/handler/mocks"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/validator"
)

func TestMessageRPC_VerifiedIdentityAndMapping(t *testing.T) {
	messages, auth := mocks.NewMockMessageService(t), mocks.NewMockIdentityClient(t)
	h := handler.NewGRPCHandler(nil, messages, auth, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	a, room, key := uuid.NewString(), uuid.NewString(), uuid.NewString()
	ctx := authorizedContext()
	auth.EXPECT().ValidateToken(ctx, mock.Anything).Return(&authv1.ValidateTokenResponse{Valid: true, UserId: a}, nil).Times(2)
	want := &domain.Message{ID: uuid.NewString(), RoomID: room, SenderID: a, ClientID: key, Text: "hi", CreatedAt: time.Now()}
	messages.EXPECT().SendMessage(ctx, a, room, key, "hi").Return(want, nil).Once()
	got, err := h.SendMessage(ctx, &chatv1.SendMessageRequest{RoomId: room, ClientMessageId: key, Text: "hi"})
	require.NoError(t, err)
	require.Equal(t, a, got.GetMessage().GetSenderId())
	require.Equal(t, key, got.GetMessage().GetClientMessageId())
	messages.EXPECT().ListMessages(ctx, a, room, "5", 1).Return(&service.MessagePage{Messages: []*domain.Message{want}, NextCursor: "3"}, nil).Once()
	page, err := h.ListMessages(ctx, &chatv1.ListMessagesRequest{RoomId: room, Cursor: "5", Limit: 1})
	require.NoError(t, err)
	require.Len(t, page.Messages, 1)
	require.Equal(t, "3", page.NextCursor)
	_, err = h.SendMessage(context.Background(), nil)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	_, err = h.ListMessages(context.Background(), nil)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestMessageRPC_SafeErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code codes.Code
	}{
		{domain.ErrMessageConflict, codes.AlreadyExists}, {domain.ErrNotFound, codes.NotFound},
		{validator.ErrInvalidText, codes.InvalidArgument}, {validator.ErrInvalidCursor, codes.InvalidArgument},
		{service.ErrInvalidLimit, codes.InvalidArgument}, {context.DeadlineExceeded, codes.DeadlineExceeded},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			messages, auth := mocks.NewMockMessageService(t), mocks.NewMockIdentityClient(t)
			h := handler.NewGRPCHandler(nil, messages, auth, slog.New(slog.NewJSONHandler(io.Discard, nil)))
			a := uuid.NewString()
			auth.EXPECT().ValidateToken(mock.Anything, mock.Anything).Return(&authv1.ValidateTokenResponse{Valid: true, UserId: a}, nil).Times(2)
			messages.EXPECT().SendMessage(mock.Anything, a, "room", "key", "text").Return(nil, tc.err).Once()
			_, err := h.SendMessage(authorizedContext(), &chatv1.SendMessageRequest{RoomId: "room", ClientMessageId: "key", Text: "text"})
			require.Equal(t, tc.code, status.Code(err))
			messages.EXPECT().ListMessages(mock.Anything, a, "room", "", 0).Return(nil, tc.err).Once()
			_, err = h.ListMessages(authorizedContext(), &chatv1.ListMessagesRequest{RoomId: "room"})
			require.Equal(t, tc.code, status.Code(err))
		})
	}
}

func TestMessageRPC_CatchUpAfterPositionWithSequences(t *testing.T) {
	messages, auth := mocks.NewMockMessageService(t), mocks.NewMockIdentityClient(t)
	h := handler.NewGRPCHandler(nil, messages, auth, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	a, room := uuid.NewString(), uuid.NewString()
	ctx := authorizedContext()
	auth.EXPECT().ValidateToken(ctx, mock.Anything).Return(&authv1.ValidateTokenResponse{Valid: true, UserId: a}, nil).Times(4)
	missed := &domain.Message{ID: uuid.NewString(), RoomID: room, SenderID: a, Text: "missed", Sequence: 8, CreatedAt: time.Now()}
	messages.EXPECT().CatchUp(ctx, a, room, "7", 2).Return(&service.MessagePage{Messages: []*domain.Message{missed}, NextCursor: "8"}, nil).Once()
	page, err := h.ListMessages(ctx, &chatv1.ListMessagesRequest{RoomId: room, After: "7", Limit: 2})
	require.NoError(t, err)
	require.Equal(t, int64(8), page.GetMessages()[0].GetSequence())
	require.Equal(t, "8", page.GetNextCursor())
	messages.EXPECT().CatchUp(ctx, a, room, "0", 0).Return(nil, validator.ErrInvalidCursor).Once()
	_, err = h.ListMessages(ctx, &chatv1.ListMessagesRequest{RoomId: room, After: "0"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	messages.EXPECT().ListMessages(ctx, a, room, "", 0).Return(&service.MessagePage{Messages: []*domain.Message{missed}}, nil).Once()
	page, err = h.ListMessages(ctx, &chatv1.ListMessagesRequest{RoomId: room})
	require.NoError(t, err)
	require.Equal(t, int64(8), page.GetMessages()[0].GetSequence())
	// One position per request: a cursor and a catch-up position contradict each other.
	_, err = h.ListMessages(ctx, &chatv1.ListMessagesRequest{RoomId: room, Cursor: "9", After: "7"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
