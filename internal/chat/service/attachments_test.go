package service_test

import (
	"context"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/service/mocks"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
)

type attachmentRepo struct {
	*mocks.MockMessageRepository
	calls int
}

func (r *attachmentRepo) SendMessageWithAttachments(_ context.Context, room, actor, key, text string, ids []string) (*domain.Message, error) {
	r.calls++
	return &domain.Message{RoomID: room, SenderID: actor, ClientID: key, Text: text, AttachmentIDs: ids}, nil
}

type checker struct {
	err   error
	calls int
}

func (c *checker) CheckAttachment(context.Context, string, string, string) error {
	c.calls++
	return c.err
}
func TestAttachments_ValidationBeforeStorage(t *testing.T) {
	a, room, key, id := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	repo := &attachmentRepo{MockMessageRepository: mocks.NewMockMessageRepository(t)}
	check := &checker{}
	svc := service.NewMessageService(repo, service.WithAttachmentChecker(check))
	ctx := context.Background()
	got, err := svc.SendAttachments(ctx, a, room, key, "", []string{id})
	require.NoError(t, err)
	require.Equal(t, []string{id}, got.AttachmentIDs)
	require.Equal(t, 1, repo.calls)
	for _, ids := range [][]string{{id, id}, {"bad"}, {id, id, id, id, id, id}} {
		_, err := svc.SendAttachments(ctx, a, room, key, "hello", ids)
		require.Error(t, err)
	}
	_, err = svc.SendAttachments(ctx, a, room, key, " ", []string{id})
	require.Error(t, err)
	check.err = service.ErrInvalidAttachment
	_, err = svc.SendAttachments(ctx, a, room, key, "hello", []string{id})
	require.ErrorIs(t, err, service.ErrInvalidAttachment)
	require.Equal(t, 1, repo.calls)
	_, err = service.NewMessageService(repo).SendAttachments(ctx, a, room, key, "hello", []string{id})
	require.ErrorIs(t, err, service.ErrMediaUnavailable)
}
