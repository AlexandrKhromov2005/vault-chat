package handler

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
)

func TestAuthFrame(t *testing.T) {
	token, err := authFrame([]byte(`{"type":"auth","token":"header.payload.signature"}`))
	require.NoError(t, err)
	require.Equal(t, "header.payload.signature", token)
	for _, frame := range []string{
		``, `{}`, `null`, `[]`, `"auth"`, `{"type":"auth"}`, `{"type":"auth","token":""}`,
		`{"type":"ping","token":"t"}`, `{"type":"AUTH","token":"t"}`, `{"token":"t"}`,
		`{"type":"auth","token":"t","user_id":"u"}`, `{"type":"auth","token":"t"} {}`, `{"type":"auth","token":"t"`,
		`{"type":"auth","token":"a b"}`, `{"type":"auth","token":"a\u0000"}`, `{"type":"auth","token":"токен"}`, `{"type":"auth","token":7}`,
	} {
		_, err := authFrame([]byte(frame))
		require.Error(t, err, frame)
	}
}

func FuzzAuthFrame(f *testing.F) {
	f.Add([]byte(`{"type":"auth","token":"a.b.c"}`))
	f.Add([]byte(`{"type":"auth","token":"a b"}`))
	f.Add([]byte(`{"type":"auth","token":"t","extra":1}`))
	f.Fuzz(func(t *testing.T, frame []byte) {
		token, err := authFrame(frame)
		if err != nil {
			require.Empty(t, token)
			return
		}
		require.NotEmpty(t, token)
		for i := 0; i < len(token); i++ {
			require.True(t, token[i] > ' ' && token[i] < 0x7f, "token byte %q", token[i])
		}
		again, err := authFrame([]byte(`{"type":"auth","token":"` + token + `"}`))
		if !strings.ContainsAny(token, `"\`) {
			require.NoError(t, err)
			require.Equal(t, token, again)
		}
	})
}

func TestEventFrame(t *testing.T) {
	created := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	message := &chatv1.Message{Id: "m", RoomId: "r", SenderId: "s", ClientMessageId: "c", Text: "hi", Sequence: 3, CreatedAt: timestamppb.New(created)}
	change := &chatv1.MemberChange{RoomId: "r", UserId: "u"}
	for _, tc := range []struct {
		event *chatv1.Event
		want  string
	}{
		{&chatv1.Event{Kind: &chatv1.Event_MessageCreated{MessageCreated: message}},
			`{"type":"message.created","room_id":"r","message":{"message_id":"m","room_id":"r","sender_id":"s","client_message_id":"c","text":"hi","sequence":3,"created_at":"2026-10-15T12:00:00Z"}}`},
		{&chatv1.Event{Kind: &chatv1.Event_MemberAdded{MemberAdded: change}}, `{"type":"member.added","room_id":"r","user_id":"u"}`},
		{&chatv1.Event{Kind: &chatv1.Event_MemberRemoved{MemberRemoved: change}}, `{"type":"member.removed","room_id":"r","user_id":"u"}`},
		{&chatv1.Event{Kind: &chatv1.Event_MemberLeft{MemberLeft: change}}, `{"type":"member.left","room_id":"r","user_id":"u"}`},
	} {
		payload, err := proto.Marshal(tc.event)
		require.NoError(t, err)
		frame, known, err := eventFrame(payload)
		require.NoError(t, err)
		require.True(t, known)
		require.JSONEq(t, tc.want, string(frame))
	}
	// Kinds added by a newer Chat are skipped rather than treated as errors.
	unknown, err := proto.Marshal(&chatv1.Event{})
	require.NoError(t, err)
	_, known, err := eventFrame(unknown)
	require.NoError(t, err)
	require.False(t, known)
	_, _, err = eventFrame([]byte{0xff, 0xff})
	require.Error(t, err)
}

func FuzzEventFrame(f *testing.F) {
	for _, event := range []*chatv1.Event{
		{Kind: &chatv1.Event_MessageCreated{MessageCreated: &chatv1.Message{Id: "m", Text: "hi", Sequence: 1}}},
		{Kind: &chatv1.Event_MemberLeft{MemberLeft: &chatv1.MemberChange{RoomId: "r", UserId: "u"}}},
	} {
		payload, err := proto.Marshal(event)
		require.NoError(f, err)
		f.Add(payload)
	}
	f.Add([]byte{0xff})
	f.Fuzz(func(t *testing.T, payload []byte) {
		frame, known, err := eventFrame(payload)
		if err != nil || !known {
			require.Nil(t, frame)
			return
		}
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(frame, &decoded))
		require.Contains(t, []any{"message.created", "member.added", "member.removed", "member.left"}, decoded["type"])
	})
}
