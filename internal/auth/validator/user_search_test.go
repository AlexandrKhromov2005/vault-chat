package validator_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
)

func TestValidateUsernameQuery(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantErr bool
	}{
		{"single letter", "a", false},
		{"mixed case", "BoB", false},
		{"full username alphabet", "a_b-C9", false},
		{"maximum length", "a" + strings.Repeat("b", 31), false},
		{"empty", "", true},
		{"too long", "a" + strings.Repeat("b", 32), true},
		{"leading digit", "1ab", true},
		{"leading underscore", "_ab", true},
		{"surrounding space", " ab", true},
		{"inner space", "a b", true},
		{"like wildcard percent", "a%", true},
		{"like escape", `a\`, true},
		{"control character", "a\x00", true},
		{"newline", "ab\n", true},
		{"non-ascii", "алиса", true},
		{"invalid utf-8", "a\xff", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateUsernameQuery(tt.query)
			if tt.wantErr {
				require.ErrorIs(t, err, validator.ErrInvalidUsernameQuery)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestSearchLimit(t *testing.T) {
	tests := []struct {
		limit   int32
		want    int
		wantErr bool
	}{
		{0, 20, false},
		{1, 1, false},
		{50, 50, false},
		{51, 0, true},
		{-1, 0, true},
	}
	for _, tt := range tests {
		got, err := validator.SearchLimit(tt.limit)
		if tt.wantErr {
			require.ErrorIs(t, err, validator.ErrInvalidSearchLimit, tt.limit)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, tt.want, got)
	}
}

// FuzzValidateUsernameQuery verifies that every accepted query is a prefix of
// some valid username and carries no LIKE metacharacter other than '_'.
func FuzzValidateUsernameQuery(f *testing.F) {
	for _, query := range []string{"a", "Bob", "a_b-c", "", "1a", "a%", `a\`, "a b", strings.Repeat("a", 40)} {
		f.Add(query)
	}
	f.Fuzz(func(t *testing.T, query string) {
		if validator.ValidateUsernameQuery(query) != nil {
			return
		}
		require.NotContains(t, query, "%")
		require.NotContains(t, query, `\`)
		username := query
		if len(username) < 3 {
			username += strings.Repeat("a", 3-len(username))
		}
		require.NoError(t, validator.ValidateUsername(username), query)
	})
}
