package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/service/mocks"
)

func TestDirect_CanonicalizesParticipantsAndRejectsSelf(t *testing.T) {
	ctx := context.WithValue(context.Background(), struct{}{}, "request-context")
	repo := mocks.NewMockRoomRepository(t)
	svc := service.NewService(repo)
	actor, peer := uuid.NewString(), uuid.NewString()
	room := &domain.Room{ID: uuid.NewString(), Kind: domain.Direct, Private: true}
	repo.EXPECT().GetOrCreateDirect(ctx, actor, peer).Return(room, nil).Once()
	got, err := svc.GetOrCreateDirect(ctx, strings.ToUpper(actor), strings.ReplaceAll(peer, "-", ""))
	require.NoError(t, err)
	require.Same(t, room, got)
	_, err = svc.GetOrCreateDirect(ctx, actor, "urn:uuid:"+actor)
	require.ErrorIs(t, err, service.ErrSelfDialog)
}
