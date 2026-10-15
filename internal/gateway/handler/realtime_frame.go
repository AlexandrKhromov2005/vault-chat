package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"

	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
)

// authFrame parses the only frame a client sends, {"type":"auth","token":...}.
// The token must be a single run of visible ASCII, like a bearer credential.
func authFrame(data []byte) (string, error) {
	var frame struct {
		Type  string `json:"type"`
		Token string `json:"token"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&frame); err != nil {
		return "", fmt.Errorf("decode client frame: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", errors.New("client frame must contain a single JSON object")
	}
	if frame.Type != "auth" || frame.Token == "" {
		return "", errors.New("client frame must be an auth frame with a token")
	}
	for i := 0; i < len(frame.Token); i++ {
		if frame.Token[i] <= ' ' || frame.Token[i] >= 0x7f {
			return "", errors.New("token must be visible ASCII")
		}
	}
	return frame.Token, nil
}

type messageFrame struct {
	Type    string          `json:"type"`
	RoomID  string          `json:"room_id"`
	Message messageResponse `json:"message"`
}

type memberFrame struct {
	Type   string `json:"type"`
	RoomID string `json:"room_id"`
	UserID string `json:"user_id"`
}

// eventFrame renders a protobuf Chat event as a JSON frame. known is false for
// kinds a newer Chat may publish that this Gateway cannot render.
func eventFrame(payload []byte) (frame []byte, known bool, err error) {
	event := &chatv1.Event{}
	if err := proto.Unmarshal(payload, event); err != nil {
		return nil, false, fmt.Errorf("decode chat event: %w", err)
	}
	var value any
	switch kind := event.GetKind().(type) {
	case *chatv1.Event_MessageCreated:
		value = messageFrame{Type: "message.created", RoomID: kind.MessageCreated.GetRoomId(), Message: messageJSON(kind.MessageCreated)}
	case *chatv1.Event_MemberAdded:
		value = memberFrame{Type: "member.added", RoomID: kind.MemberAdded.GetRoomId(), UserID: kind.MemberAdded.GetUserId()}
	case *chatv1.Event_MemberRemoved:
		value = memberFrame{Type: "member.removed", RoomID: kind.MemberRemoved.GetRoomId(), UserID: kind.MemberRemoved.GetUserId()}
	case *chatv1.Event_MemberLeft:
		value = memberFrame{Type: "member.left", RoomID: kind.MemberLeft.GetRoomId(), UserID: kind.MemberLeft.GetUserId()}
	default:
		return nil, false, nil
	}
	frame, err = json.Marshal(value)
	if err != nil {
		return nil, false, fmt.Errorf("encode event frame: %w", err)
	}
	return frame, true, nil
}
