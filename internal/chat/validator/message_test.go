package validator

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestMessageText(t *testing.T) {
	for _, text := range []string{"hello", " привет\nмир\t ", strings.Repeat("a", 4096)} {
		require.NoError(t, MessageText(text))
	}
	for _, text := range []string{"", " \n\t", "hello\x00", "bad\x1b", "\xff", strings.Repeat("a", 4097)} {
		require.ErrorIs(t, MessageText(text), ErrInvalidText)
	}
}

func TestHistoryCursor(t *testing.T) {
	for _, input := range []string{"", "1", "9223372036854775807"} {
		_, err := HistoryCursor(input)
		require.NoError(t, err)
	}
	for _, input := range []string{"0", "01", "-1", "+1", " 1", "9223372036854775808", "x"} {
		_, err := HistoryCursor(input)
		require.ErrorIs(t, err, ErrInvalidCursor)
	}
}

func FuzzMessageText(f *testing.F) {
	f.Add("hello\nworld")
	f.Add("\x00")
	f.Fuzz(func(t *testing.T, text string) {
		if MessageText(text) == nil {
			require.True(t, utf8.ValidString(text))
			require.LessOrEqual(t, len(text), 16384)
			require.InDelta(t, utf8.RuneCountInString(text), min(utf8.RuneCountInString(text), 4096), 0)
			require.NotEmpty(t, strings.TrimSpace(text))
		}
	})
}

func FuzzHistoryCursor(f *testing.F) {
	f.Add("")
	f.Add("1")
	f.Add("-1")
	f.Fuzz(func(t *testing.T, input string) {
		value, err := HistoryCursor(input)
		if err == nil && input != "" {
			require.Positive(t, value)
			require.Equal(t, input, FormatHistoryCursor(value))
		}
	})
}
