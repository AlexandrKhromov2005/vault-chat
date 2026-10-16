package validator_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/validator"
)

func TestMemberCursor(t *testing.T) {
	id := uuid.NewString()
	for _, input := range []string{"", id} {
		got, err := validator.MemberCursor(input)
		require.NoError(t, err)
		require.Equal(t, input, got)
	}
	for _, input := range []string{strings.ToUpper(id), "urn:uuid:" + id, strings.ReplaceAll(id, "-", ""), uuid.Nil.String(), " " + id, "1"} {
		_, err := validator.MemberCursor(input)
		require.ErrorIs(t, err, validator.ErrInvalidMemberCursor, input)
	}
}

func FuzzMemberCursor(f *testing.F) {
	for _, s := range []string{"", "d79cba66-65b0-43eb-8985-7dae25ed40e0", "D79CBA66-65B0-43EB-8985-7DAE25ED40E0", "bad"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		got, err := validator.MemberCursor(input)
		if err != nil {
			require.Empty(t, got)
			return
		}
		require.Equal(t, input, got)
		if input != "" {
			id, err := validator.CanonicalID(input)
			require.NoError(t, err)
			require.Equal(t, input, id)
		}
	})
}
