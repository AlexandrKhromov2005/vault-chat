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
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/validator"
)

func TestListMembers_PagesByUserID(t *testing.T) {
	ctx := context.Background()
	repo := mocks.NewMockRoomRepository(t)
	svc := service.NewService(repo)
	actor, roomID := uuid.NewString(), uuid.NewString()
	rows := []*domain.Member{{UserID: "a"}, {UserID: "b"}, {UserID: "c"}}
	repo.EXPECT().ListMembers(ctx, roomID, actor, "", 3).Return(rows, nil).Once()
	page, err := svc.ListMembers(ctx, strings.ToUpper(actor), strings.ToUpper(roomID), "", 2)
	require.NoError(t, err)
	require.Equal(t, rows[:2], page.Members)
	require.Equal(t, "b", page.NextCursor)
	cursor := uuid.NewString()
	repo.EXPECT().ListMembers(ctx, roomID, actor, cursor, 51).Return(rows[2:], nil).Once()
	page, err = svc.ListMembers(ctx, actor, roomID, cursor, 0)
	require.NoError(t, err)
	require.Equal(t, rows[2:], page.Members)
	require.Empty(t, page.NextCursor)
	repo.EXPECT().ListMembers(ctx, roomID, actor, "", 101).Return(nil, domain.ErrNotFound).Once()
	_, err = svc.ListMembers(ctx, actor, roomID, "", 100)
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestMembershipChanges_DelegateCanonicalIDs(t *testing.T) {
	ctx := context.Background()
	repo := mocks.NewMockRoomRepository(t)
	svc := service.NewService(repo)
	actor, roomID, member := uuid.NewString(), uuid.NewString(), uuid.NewString()
	repo.EXPECT().RemoveChannelMember(ctx, roomID, actor, member).Return(domain.ErrOwnerMembership).Once()
	require.ErrorIs(t, svc.RemoveMember(ctx, strings.ToUpper(actor), roomID, "urn:uuid:"+member), domain.ErrOwnerMembership)
	repo.EXPECT().LeaveRoom(ctx, roomID, actor).Return(nil).Once()
	require.NoError(t, svc.LeaveRoom(ctx, actor, strings.ToUpper(roomID)))
	repo.EXPECT().LeaveRoom(ctx, roomID, member).Return(domain.ErrForbidden).Once()
	require.ErrorIs(t, svc.LeaveRoom(ctx, member, roomID), domain.ErrForbidden)
}

func TestMemberOperations_InvalidInputsDoNotReachStorage(t *testing.T) {
	id := uuid.NewString()
	for _, tc := range []struct {
		name string
		call func(*service.Service) error
		err  error
	}{
		{"list actor", func(s *service.Service) error {
			_, err := s.ListMembers(context.Background(), "bad", id, "", 1)
			return err
		}, validator.ErrInvalidID},
		{"list room", func(s *service.Service) error {
			_, err := s.ListMembers(context.Background(), id, "bad", "", 1)
			return err
		}, validator.ErrInvalidID},
		{"list cursor", func(s *service.Service) error {
			_, err := s.ListMembers(context.Background(), id, id, strings.ToUpper(id), 1)
			return err
		}, validator.ErrInvalidMemberCursor},
		{"list limit", func(s *service.Service) error {
			_, err := s.ListMembers(context.Background(), id, id, "", 101)
			return err
		}, service.ErrInvalidMemberLimit},
		{"remove actor", func(s *service.Service) error { return s.RemoveMember(context.Background(), "bad", id, id) }, validator.ErrInvalidID},
		{"remove room", func(s *service.Service) error { return s.RemoveMember(context.Background(), id, "bad", id) }, validator.ErrInvalidID},
		{"remove user", func(s *service.Service) error { return s.RemoveMember(context.Background(), id, id, "bad") }, validator.ErrInvalidID},
		{"leave actor", func(s *service.Service) error { return s.LeaveRoom(context.Background(), "bad", id) }, validator.ErrInvalidID},
		{"leave room", func(s *service.Service) error { return s.LeaveRoom(context.Background(), id, "bad") }, validator.ErrInvalidID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := mocks.NewMockRoomRepository(t)
			require.ErrorIs(t, tc.call(service.NewService(repo)), tc.err)
			require.Empty(t, repo.Calls)
		})
	}
}
