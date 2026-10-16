// Package events encodes committed Chat changes as chatv1.Event, the wire
// form in which Gateway instances relay them to their WebSocket clients.
package events

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
)

// Encode converts an event to its protobuf wire form, chatv1.Event.
func Encode(event domain.Event) ([]byte, error) {
	wire := &chatv1.Event{}
	change := &chatv1.MemberChange{RoomId: event.RoomID, UserId: event.UserID}
	switch event.Kind {
	case domain.MessageCreated:
		m := event.Message
		if m == nil {
			return nil, fmt.Errorf("message event without a message")
		}
		wire.Kind = &chatv1.Event_MessageCreated{MessageCreated: &chatv1.Message{Id: m.ID, RoomId: m.RoomID, SenderId: m.SenderID,
			ClientMessageId: m.ClientID, Text: m.Text, CreatedAt: timestamppb.New(m.CreatedAt), Sequence: m.Sequence}}
	case domain.MemberAdded:
		wire.Kind = &chatv1.Event_MemberAdded{MemberAdded: change}
	case domain.MemberRemoved:
		wire.Kind = &chatv1.Event_MemberRemoved{MemberRemoved: change}
	case domain.MemberLeft:
		wire.Kind = &chatv1.Event_MemberLeft{MemberLeft: change}
	default:
		return nil, fmt.Errorf("unknown event kind %q", event.Kind)
	}
	payload, err := proto.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}
	return payload, nil
}
