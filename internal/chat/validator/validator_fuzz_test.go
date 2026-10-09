package validator_test

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/validator"
)

func FuzzCanonicalID(f *testing.F) {
	for _, s := range []string{"", "bad", "00000000-0000-0000-0000-000000000000", "D79CBA66-65B0-43EB-8985-7DAE25ED40E0"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		got, err := validator.CanonicalID(input)
		if err != nil {
			require.Empty(t, got)
			return
		}
		id, err := uuid.Parse(got)
		require.NoError(t, err)
		require.NotEqual(t, uuid.Nil, id)
		require.Equal(t, id.String(), got)
		again, err := validator.CanonicalID(got)
		require.NoError(t, err)
		require.Equal(t, got, again)
	})
}

func FuzzChannelName(f *testing.F) {
	for _, s := range []string{"general", "Команда", "", "a\nb", strings.Repeat("x", 81)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if validator.ChannelName(name) != nil {
			return
		}
		require.True(t, utf8.ValidString(name))
		require.GreaterOrEqual(t, utf8.RuneCountInString(name), 1)
		require.LessOrEqual(t, utf8.RuneCountInString(name), 80)
		require.Equal(t, strings.TrimSpace(name), name)
		for _, r := range name {
			require.False(t, unicode.IsControl(r))
		}
	})
}
