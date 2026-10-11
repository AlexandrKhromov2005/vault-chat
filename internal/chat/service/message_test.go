package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/service/mocks"
)

func TestMessages_ValidationAndDelegation(t *testing.T) {
	ctx := context.Background()
	a, room, key := uuid.NewString(), uuid.NewString(), uuid.NewString()
	repo := mocks.NewMockMessageRepository(t)
	svc := service.NewMessageService(repo)
	want := &domain.Message{ID: uuid.NewString(), Text: "hello"}
	repo.EXPECT().SendMessage(ctx, room, a, key, "hello").Return(want, nil).Once()
	got, err := svc.SendMessage(ctx, "urn:uuid:"+a, room, key, "hello")
	require.NoError(t, err)
	require.Equal(t, want, got)
	for _, args := range [][4]string{{"bad",room,key,"hello"},{a,"bad",key,"hello"},{a,room,"bad","hello"},{a,room,key," "}} {
		_, err := svc.SendMessage(ctx, args[0],args[1],args[2],args[3])
		require.Error(t, err)
	}
	repo.EXPECT().SendMessage(ctx, room, a, key, "changed").Return(nil, domain.ErrMessageConflict).Once()
	_, err = svc.SendMessage(ctx,a,room,key,"changed")
	require.ErrorIs(t,err,domain.ErrMessageConflict)
}

func TestMessages_HistoryPages(t *testing.T) {
	ctx := context.Background()
	a, room := uuid.NewString(), uuid.NewString()
	repo := mocks.NewMockMessageRepository(t)
	svc := service.NewMessageService(repo)
	rows := []*domain.Message{{Sequence:3},{Sequence:2},{Sequence:1}}
	repo.EXPECT().ListMessages(ctx,room,a,int64(0),3).Return(rows,nil).Once()
	page, err := svc.ListMessages(ctx,a,room,"",2)
	require.NoError(t,err)
	require.Equal(t,rows[:2],page.Messages)
	require.Equal(t,"2",page.NextCursor)
	repo.EXPECT().ListMessages(ctx,room,a,int64(2),3).Return(rows[2:],nil).Once()
	page, err = svc.ListMessages(ctx,a,room,page.NextCursor,2)
	require.NoError(t,err)
	require.Len(t,page.Messages,1)
	require.Empty(t,page.NextCursor)
	repo.EXPECT().ListMessages(ctx,room,a,int64(0),51).Return([]*domain.Message{},nil).Once()
	page, err = svc.ListMessages(ctx,a,room,"",0)
	require.NoError(t,err)
	require.Empty(t,page.Messages)
	for _, args := range []struct{ actor,room,cursor string; limit int }{{"bad",room,"",1},{a,"bad","",1},{a,room,"01",1},{a,room,"",-1},{a,room,"",101}} {
		_, err := svc.ListMessages(ctx,args.actor,args.room,args.cursor,args.limit)
		require.Error(t,err)
	}
	repo.EXPECT().ListMessages(mock.Anything,room,a,int64(0),2).Return(nil,context.Canceled).Once()
	_, err = svc.ListMessages(ctx,a,room,"",1)
	require.True(t,errors.Is(err,context.Canceled))
}
