package validator_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
)

func TestCanonicalUserID(t *testing.T) {
	id := uuid.NewString()
	got, err := validator.CanonicalUserID(strings.ToUpper(id))
	require.NoError(t, err)
	require.Equal(t, id, got)
	for _, input := range []string{"", "bad", uuid.Nil.String()} {
		_, err := validator.CanonicalUserID(input)
		require.ErrorIs(t, err, validator.ErrInvalidUserID)
	}
}

func FuzzCanonicalUserID(f *testing.F) {
	for _, input := range []string{"", "bad", uuid.Nil.String(), "57CF7798-BB7F-42A4-BC04-C48B502F1A38"} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		got, err := validator.CanonicalUserID(input)
		if err != nil {
			require.Empty(t, got)
			return
		}
		id, err := uuid.Parse(got)
		require.NoError(t, err)
		require.NotEqual(t, uuid.Nil, id)
		require.Equal(t, id.String(), got)
	})
}
