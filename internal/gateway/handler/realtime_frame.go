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
	if len(bytes.TrimSpace(payload)) > 0 && bytes.TrimSpace(payload)[0] == '{' {
		return callEventFrame(payload)
	}
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

type callSignalFrame struct {
	SignalSeq int64  `json:"signal_seq"`
	SenderSeq int64  `json:"sender_seq"`
	Kind      string `json:"kind"`
	Payload   string `json:"payload"`
}

type callEvent struct {
	Type         string           `json:"type"`
	CallID       string           `json:"call_id"`
	RoomID       string           `json:"room_id"`
	State        string           `json:"state,omitempty"`
	StateVersion int64            `json:"state_version,omitempty"`
	Signal       *callSignalFrame `json:"signal,omitempty"`
}

func callEventFrame(payload []byte) ([]byte, bool, error) {
	var event callEvent
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&event); err != nil {
		return nil, false, fmt.Errorf("decode call event: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, false, errors.New("call event must contain a single JSON object")
	}
	if len(event.CallID) == 0 || len(event.CallID) > 128 || len(event.RoomID) == 0 || len(event.RoomID) > 128 {
		return nil, false, errors.New("invalid call event identity")
	}
	switch event.Type {
	case "call.created", "call.accepted", "call.connected", "call.ended":
		if event.Signal != nil || event.StateVersion < 0 || !validCallState(event.State) {
			return nil, false, errors.New("invalid call state event")
		}
	case "call.signal":
		if event.Signal == nil || !validCallSignal(event.Signal) {
			return nil, false, errors.New("invalid call signal event")
		}
	default:
		return nil, false, errors.New("unknown call event")
	}
	frame, err := json.Marshal(event)
	if err != nil {
		return nil, false, fmt.Errorf("encode call event: %w", err)
	}
	return frame, true, nil
}

func validCallState(state string) bool {
	switch state {
	case "ringing", "connecting", "active", "ended":
		return true
	default:
		return false
	}
}

func validCallSignal(signal *callSignalFrame) bool {
	if signal.SignalSeq <= 0 || signal.SenderSeq <= 0 {
		return false
	}
	switch signal.Kind {
	case "offer", "answer":
		return len(signal.Payload) > 0 && len(signal.Payload) <= 8192
	case "candidate":
		return len(signal.Payload) > 0 && len(signal.Payload) <= 512
	case "end-of-candidates":
		return signal.Payload == ""
	default:
		return false
	}
}
