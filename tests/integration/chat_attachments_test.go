package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/repository"
)

func TestChatAttachments_PersistenceAndRetry(t *testing.T) {
	repo := repository.NewRoomRepository(newChatPool(t))
	ctx := context.Background()
	a, b := uuid.NewString(), uuid.NewString()
	room, err := repo.GetOrCreateDirect(ctx, a, b)
	require.NoError(t, err)
	key := uuid.NewString()
	ids := []string{uuid.NewString()}
	sent, err := repo.SendMessageWithAttachments(ctx, room.ID, a, key, "", ids)
	require.NoError(t, err)
	require.Equal(t, ids, sent.AttachmentIDs)
	retry, err := repo.SendMessageWithAttachments(ctx, room.ID, a, key, "", ids)
	require.NoError(t, err)
	require.Equal(t, sent, retry)
	_, err = repo.SendMessage(ctx, room.ID, a, key, "")
	require.ErrorIs(t, err, domain.ErrMessageConflict)
	_, err = repo.SendMessageWithAttachments(ctx, room.ID, a, key, "", []string{uuid.NewString()})
	require.ErrorIs(t, err, domain.ErrMessageConflict)
	page, err := repo.ListMessagesAfter(ctx, room.ID, b, 0, 10)
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Equal(t, sent, page[0])
}
