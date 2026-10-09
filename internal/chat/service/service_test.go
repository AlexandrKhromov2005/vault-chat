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

func TestChannelOperations_PreserveActorAndAccessErrors(t *testing.T) {
	ctx := context.Background()
	repo := mocks.NewMockRoomRepository(t)
	svc := service.NewService(repo)
	actor, member, roomID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	room := &domain.Room{ID: roomID, Kind: domain.Channel, OwnerID: actor, Name: "general"}
	repo.EXPECT().CreateChannel(ctx, actor, "general", true).Return(room, nil).Once()
	got, err := svc.CreateChannel(ctx, strings.ToUpper(actor), "general", true)
	require.NoError(t, err)
	require.Same(t, room, got)
	repo.EXPECT().GetForMember(ctx, roomID, actor).Return(room, nil).Once()
	got, err = svc.GetRoom(ctx, strings.ToUpper(actor), strings.ToUpper(roomID))
	require.NoError(t, err)
	require.Same(t, room, got)
	repo.EXPECT().AddChannelMember(ctx, roomID, actor, member).Return(domain.ErrForbidden).Once()
	require.ErrorIs(t, svc.AddMember(ctx, actor, roomID, strings.ToUpper(member)), domain.ErrForbidden)
	repo.EXPECT().GetForMember(ctx, roomID, member).Return(nil, domain.ErrNotFound).Once()
	_, err = svc.GetRoom(ctx, member, roomID)
	require.ErrorIs(t, err, domain.ErrNotFound)
}
