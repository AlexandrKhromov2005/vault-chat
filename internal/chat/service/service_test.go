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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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

func TestInvalidInputs_DoNotReachStorage(t *testing.T) {
	actor, peer, roomID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	tests := []struct {
		name string
		call func(*service.Service) error
	}{
		{"direct actor", func(s *service.Service) error {
			_, err := s.GetOrCreateDirect(context.Background(), "bad", peer)
			return err
		}},
		{"direct peer", func(s *service.Service) error {
			_, err := s.GetOrCreateDirect(context.Background(), actor, uuid.Nil.String())
			return err
		}},
		{"channel actor", func(s *service.Service) error {
			_, err := s.CreateChannel(context.Background(), "bad", "general", false)
			return err
		}},
		{"channel name", func(s *service.Service) error {
			_, err := s.CreateChannel(context.Background(), actor, "a\nb", false)
			return err
		}},
		{"read actor", func(s *service.Service) error { _, err := s.GetRoom(context.Background(), "bad", roomID); return err }},
		{"read room", func(s *service.Service) error { _, err := s.GetRoom(context.Background(), actor, "bad"); return err }},
		{"add actor", func(s *service.Service) error { return s.AddMember(context.Background(), "bad", roomID, peer) }},
		{"add room", func(s *service.Service) error { return s.AddMember(context.Background(), actor, "bad", peer) }},
		{"add user", func(s *service.Service) error { return s.AddMember(context.Background(), actor, roomID, "bad") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := mocks.NewMockRoomRepository(t)
			require.Error(t, tt.call(service.NewService(repo)))
			require.Empty(t, repo.Calls)
		})
	}
}

func TestStorageFailures_AreWrappedAndRemainDetectable(t *testing.T) {
	ctx := context.Background()
	repo := mocks.NewMockRoomRepository(t)
	svc := service.NewService(repo)
	actor, peer := uuid.NewString(), uuid.NewString()
	repo.EXPECT().GetOrCreateDirect(ctx, actor, peer).Return(nil, context.DeadlineExceeded).Once()
	_, err := svc.GetOrCreateDirect(ctx, actor, peer)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	repo.EXPECT().CreateChannel(ctx, actor, "general", false).Return(nil, context.Canceled).Once()
	_, err = svc.CreateChannel(ctx, actor, "general", false)
	require.ErrorIs(t, err, context.Canceled)
	repo.EXPECT().AddChannelMember(ctx, peer, actor, peer).Return(nil).Once()
	require.NoError(t, svc.AddMember(ctx, actor, peer, peer))
}
