package events_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/events"
)

func TestEncode_EveryKind(t *testing.T) {
	room, user := uuid.NewString(), uuid.NewString()
	created := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	message := &domain.Message{ID: uuid.NewString(), RoomID: room, SenderID: user, ClientID: uuid.NewString(), Text: "hi", Sequence: 7, CreatedAt: created}
	change := &chatv1.MemberChange{RoomId: room, UserId: user}
	for _, tc := range []struct {
		event domain.Event
		want  *chatv1.Event
	}{
		{domain.Event{Kind: domain.MessageCreated, RoomID: room, Message: message}, &chatv1.Event{Kind: &chatv1.Event_MessageCreated{MessageCreated: &chatv1.Message{
			Id: message.ID, RoomId: room, SenderId: user, ClientMessageId: message.ClientID, Text: "hi", Sequence: 7, CreatedAt: timestamppb.New(created),
		}}}},
		{domain.Event{Kind: domain.MemberAdded, RoomID: room, UserID: user}, &chatv1.Event{Kind: &chatv1.Event_MemberAdded{MemberAdded: change}}},
		{domain.Event{Kind: domain.MemberRemoved, RoomID: room, UserID: user}, &chatv1.Event{Kind: &chatv1.Event_MemberRemoved{MemberRemoved: change}}},
		{domain.Event{Kind: domain.MemberLeft, RoomID: room, UserID: user}, &chatv1.Event{Kind: &chatv1.Event_MemberLeft{MemberLeft: change}}},
	} {
		t.Run(string(tc.event.Kind), func(t *testing.T) {
			payload, err := events.Encode(tc.event)
			require.NoError(t, err)
			got := &chatv1.Event{}
			require.NoError(t, proto.Unmarshal(payload, got))
			require.True(t, proto.Equal(tc.want, got), "got %v", got)
		})
	}
	for _, event := range []domain.Event{{Kind: "room.renamed", RoomID: room}, {Kind: domain.MessageCreated, RoomID: room}} {
		_, err := events.Encode(event)
		require.Error(t, err)
	}
}
