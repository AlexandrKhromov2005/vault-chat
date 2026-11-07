package eventbus_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/eventbus"
)

func ids(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = uuid.NewString()
	}
	return out
}

func TestPushSignalsRoundTripAndSplit(t *testing.T) {
	room := uuid.NewString()
	users := ids(eventbus.MaxPushSignalRecipients*2 + 1)
	payloads, err := eventbus.EncodePushSignals(room, users)
	require.NoError(t, err)
	require.Len(t, payloads, 3)
	var got []string
	for _, payload := range payloads {
		require.NotContains(t, string(payload), "text")
		signal, err := eventbus.DecodePushSignal(payload)
		require.NoError(t, err)
		require.Equal(t, room, signal.RoomID)
		got = append(got, signal.UserIDs...)
	}
	require.Equal(t, users, got)

	payloads, err = eventbus.EncodePushSignals(room, nil)
	require.NoError(t, err)
	require.Empty(t, payloads, "no recipients, no signal")
}

func TestDecodePushSignalRejectsMalformedInput(t *testing.T) {
	room, user := uuid.NewString(), uuid.NewString()
	tooMany := fmt.Sprintf(`{"room_id":%q,"user_ids":["%s"]}`, room, strings.Join(ids(eventbus.MaxPushSignalRecipients+1), `","`))
	for _, payload := range []string{
		``, `!`, `[]`, `{}`, `null`,
		fmt.Sprintf(`{"room_id":%q}`, room),
		fmt.Sprintf(`{"room_id":%q,"user_ids":[]}`, room),
		fmt.Sprintf(`{"room_id":"r","user_ids":[%q]}`, user),
		fmt.Sprintf(`{"room_id":%q,"user_ids":["u"]}`, room),
		fmt.Sprintf(`{"room_id":%q,"user_ids":[%q]}`, strings.ToUpper(room), user),
		fmt.Sprintf(`{"room_id":%q,"user_ids":[%q]}`, room, "{"+user+"}"),
		fmt.Sprintf(`{"room_id":"00000000-0000-0000-0000-000000000000","user_ids":[%q]}`, user),
		fmt.Sprintf(`{"room_id":%q,"user_ids":[1]}`, room),
		tooMany,
	} {
		_, err := eventbus.DecodePushSignal([]byte(payload))
		require.ErrorIs(t, err, eventbus.ErrInvalidPushSignal, payload)
	}
	_, err := eventbus.DecodePushSignal([]byte(fmt.Sprintf(`{"room_id":%q,"user_ids":[%q],"text":"ignored"}`, room, user)))
	require.NoError(t, err, "unknown fields are ignored, never forwarded")
}

func FuzzDecodePushSignal(f *testing.F) {
	room, user := uuid.NewString(), uuid.NewString()
	for _, seed := range []string{fmt.Sprintf(`{"room_id":%q,"user_ids":[%q]}`, room, user), `{}`, `!`, `{"user_ids":null}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, payload []byte) {
		signal, err := eventbus.DecodePushSignal(payload)
		if err != nil {
			return
		}
		if len(signal.UserIDs) == 0 || len(signal.UserIDs) > eventbus.MaxPushSignalRecipients {
			t.Fatalf("accepted %d recipients", len(signal.UserIDs))
		}
		again, err := eventbus.EncodePushSignals(signal.RoomID, signal.UserIDs)
		if err != nil || len(again) != 1 {
			t.Fatalf("accepted signal does not re-encode: %v", err)
		}
	})
}
