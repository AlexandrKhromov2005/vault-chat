package eventbus_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/eventbus"
)

func TestUserChannel(t *testing.T) {
	require.Equal(t, "chat:user:alice", eventbus.UserChannel("alice"))
}

func TestUserFromChannel(t *testing.T) {
	tests := []struct {
		name    string
		channel string
		user    string
		ok      bool
	}{
		{"user channel", "chat:user:alice", "alice", true},
		{"round trip", eventbus.UserChannel("3f1c2d4e-5a6b-4c7d-8e9f-0a1b2c3d4e5f"), "3f1c2d4e-5a6b-4c7d-8e9f-0a1b2c3d4e5f", true},
		{"other channel", "chat:room:alice", "", false},
		{"bare prefix", "chat:user:", "", false},
		{"empty", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user, ok := eventbus.UserFromChannel(tt.channel)
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.user, user)
		})
	}
}

func FuzzUserFromChannel(f *testing.F) {
	for _, channel := range []string{
		"chat:user:alice",
		eventbus.UserChannel("3f1c2d4e-5a6b-4c7d-8e9f-0a1b2c3d4e5f"),
		"chat:user:chat:user:alice",
		"chat:user: ",
		"chat:user:",
		"chat:user",
		"chat:room:alice",
		"CHAT:USER:alice",
		" chat:user:alice",
		"",
	} {
		f.Add(channel)
	}
	prefix := eventbus.UserChannel("")
	f.Fuzz(func(t *testing.T, channel string) {
		user, ok := eventbus.UserFromChannel(channel)
		// Exactly the channels with a wrong prefix or an empty suffix are rejected.
		require.Equal(t, strings.HasPrefix(channel, prefix) && channel != prefix, ok, "channel %q", channel)
		if !ok {
			require.Empty(t, user)
			return
		}
		require.NotEmpty(t, user)
		require.Equal(t, channel, eventbus.UserChannel(user))
	})
}
