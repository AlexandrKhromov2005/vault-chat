package service_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
)

// fastParams keeps unit tests quick; production uses DefaultArgon2idParams.
var fastParams = service.Argon2idParams{
	MemoryKiB:   1024,
	Time:        1,
	Parallelism: 2,
	SaltLength:  16,
	KeyLength:   32,
}

func TestArgon2idHasher_Hash(t *testing.T) {
	hasher := service.NewArgon2idHasher(fastParams)

	t.Run("returns PHC formatted string", func(t *testing.T) {
		encoded, err := hasher.Hash("password1")
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(encoded, "$argon2id$v=19$"),
			"expected PHC string format, got %q", encoded)
		require.Contains(t, encoded, "m=1024,t=1,p=2")
	})

	t.Run("uses a random salt per call", func(t *testing.T) {
		first, err := hasher.Hash("password1")
		require.NoError(t, err)
		second, err := hasher.Hash("password1")
		require.NoError(t, err)
		require.NotEqual(t, first, second)
	})

	t.Run("rejects empty password", func(t *testing.T) {
		_, err := hasher.Hash("")
		require.Error(t, err)
	})
}

func TestArgon2idHasher_Verify(t *testing.T) {
	hasher := service.NewArgon2idHasher(fastParams)

	valid, err := hasher.Hash("correct horse battery staple 9")
	require.NoError(t, err)

	tests := []struct {
		name      string
		password  string
		hash      string
		wantMatch bool
		wantErr   bool
	}{
		{"correct password", "correct horse battery staple 9", valid, true, false},
		{"wrong password", "correct horse battery staple 8", valid, false, false},
		{"empty password against valid hash", "", valid, false, false},
		{"empty hash", "password1", "", false, true},
		{"garbage hash", "password1", "not-a-phc-string", false, true},
		{"truncated hash", "password1", valid[:len(valid)/2], false, true},
		{"unsupported algorithm", "password1", "$argon2i$v=19$m=1024,t=1,p=2$c2FsdA$a2V5", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match, err := hasher.Verify(tt.password, tt.hash)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantMatch, match)
		})
	}
}

func TestArgon2idHasher_Verify_ParamsRoundTrip(t *testing.T) {
	// A hash produced with one parameter set must verify against a hasher
	// configured with different parameters — parameters live in the PHC string.
	fast := service.NewArgon2idHasher(fastParams)
	other := service.NewArgon2idHasher(service.Argon2idParams{
		MemoryKiB:   2048,
		Time:        2,
		Parallelism: 1,
		SaltLength:  16,
		KeyLength:   32,
	})

	encoded, err := fast.Hash("password1")
	require.NoError(t, err)

	match, err := other.Verify("password1", encoded)
	require.NoError(t, err)
	require.True(t, match)
}
