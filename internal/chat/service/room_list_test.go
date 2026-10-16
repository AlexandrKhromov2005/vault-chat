package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/service/mocks"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/validator"
)

func TestListRooms_PagesWithLookahead(t *testing.T) {
	ctx := context.Background()
	repo := mocks.NewMockRoomRepository(t)
	svc := service.NewService(repo)
	actor := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Microsecond)
	rows := []*domain.RoomSummary{
		{Room: domain.Room{ID: uuid.NewString()}, Activity: now},
		{Room: domain.Room{ID: uuid.NewString()}, Activity: now.Add(-time.Second)},
		{Room: domain.Room{ID: uuid.NewString()}, Activity: now.Add(-2 * time.Second)},
	}
	repo.EXPECT().ListForMember(ctx, actor, domain.RoomPosition{}, 3).Return(rows, nil).Once()
	page, err := svc.ListRooms(ctx, strings.ToUpper(actor), "", 2)
	require.NoError(t, err)
	require.Equal(t, rows[:2], page.Rooms)
	require.Equal(t, validator.FormatRoomCursor(domain.RoomPosition{Activity: rows[1].Activity, ID: rows[1].ID}), page.NextCursor)
	position, err := validator.RoomCursor(page.NextCursor)
	require.NoError(t, err)
	repo.EXPECT().ListForMember(ctx, actor, position, 3).Return(rows[2:], nil).Once()
	page, err = svc.ListRooms(ctx, actor, page.NextCursor, 2)
	require.NoError(t, err)
	require.Equal(t, rows[2:], page.Rooms)
	require.Empty(t, page.NextCursor)
	repo.EXPECT().ListForMember(ctx, actor, domain.RoomPosition{}, 51).Return([]*domain.RoomSummary{}, nil).Once()
	page, err = svc.ListRooms(ctx, actor, "", 0)
	require.NoError(t, err)
	require.Empty(t, page.Rooms)
	require.Empty(t, page.NextCursor)
	repo.EXPECT().ListForMember(mock.Anything, actor, domain.RoomPosition{}, 101).Return(nil, context.Canceled).Once()
	_, err = svc.ListRooms(ctx, actor, "", 100)
	require.ErrorIs(t, err, context.Canceled)
}

func TestListRooms_InvalidInputsDoNotReachStorage(t *testing.T) {
	actor := uuid.NewString()
	for _, tc := range []struct {
		name, actor, cursor string
		limit               int
		err                 error
	}{
		{"actor", "bad", "", 1, validator.ErrInvalidID},
		{"cursor", actor, "1_" + strings.ToUpper(uuid.NewString()), 1, validator.ErrInvalidRoomCursor},
		{"negative limit", actor, "", -1, service.ErrInvalidRoomLimit},
		{"large limit", actor, "", 101, service.ErrInvalidRoomLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := mocks.NewMockRoomRepository(t)
			_, err := service.NewService(repo).ListRooms(context.Background(), tc.actor, tc.cursor, tc.limit)
			require.ErrorIs(t, err, tc.err)
			require.Empty(t, repo.Calls)
		})
	}
}
