package eventbus_test

import (
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
