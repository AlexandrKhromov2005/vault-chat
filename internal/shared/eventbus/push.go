package eventbus

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// PushSignalChannel carries the wake-up signals Chat publishes for new
// messages and Auth turns into pushes (docs/push.md). A signal names a room
// and recipients only: no text, names or sender.
const PushSignalChannel = "push:signal:v1"

// MaxPushSignalRecipients bounds one signal; Chat splits larger rooms.
const MaxPushSignalRecipients = 500

// maxPushSignalBytes bounds a signal: 500 quoted UUIDs and the room.
const maxPushSignalBytes = 24 << 10

// ErrInvalidPushSignal rejects signals that are malformed or too large.
var ErrInvalidPushSignal = errors.New("eventbus: invalid push signal")

// PushSignal asks Auth to wake the devices of UserIDs to synchronize RoomID.
type PushSignal struct {
	RoomID  string   `json:"room_id"`
	UserIDs []string `json:"user_ids"`
}

// EncodePushSignals splits recipients into signals of at most
// MaxPushSignalRecipients each.
func EncodePushSignals(roomID string, userIDs []string) ([][]byte, error) {
	var payloads [][]byte
	for start := 0; start < len(userIDs); start += MaxPushSignalRecipients {
		signal := PushSignal{RoomID: roomID, UserIDs: userIDs[start:min(start+MaxPushSignalRecipients, len(userIDs))]}
		if err := signal.validate(); err != nil {
			return nil, err
		}
		payload, err := json.Marshal(signal)
		if err != nil {
			return nil, fmt.Errorf("encode push signal: %w", err)
		}
		payloads = append(payloads, payload)
	}
	return payloads, nil
}

// DecodePushSignal parses a signal from the channel, which anyone with Redis
// access can write to, so every field is checked.
func DecodePushSignal(payload []byte) (PushSignal, error) {
	var signal PushSignal
	if len(payload) > maxPushSignalBytes || json.Unmarshal(payload, &signal) != nil {
		return PushSignal{}, ErrInvalidPushSignal
	}
	if err := signal.validate(); err != nil {
		return PushSignal{}, err
	}
	return signal, nil
}

func (s PushSignal) validate() error {
	if !canonicalUUID(s.RoomID) || len(s.UserIDs) == 0 || len(s.UserIDs) > MaxPushSignalRecipients {
		return ErrInvalidPushSignal
	}
	for _, id := range s.UserIDs {
		if !canonicalUUID(id) {
			return ErrInvalidPushSignal
		}
	}
	return nil
}

// canonicalUUID accepts only the lower-case hyphenated form, so that one
// user cannot appear under several spellings.
func canonicalUUID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id != uuid.Nil && id.String() == s
}
