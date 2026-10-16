package repository

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsernamePrefixRange(t *testing.T) {
	tests := []struct {
		prefix, lower, upper string
	}{
		{"bo", "bo", "bp"},
		{"BoB", "bob", "boc"},
		{"al_", "al_", "al`"},
		{"a-", "a-", "a."},
		{"a9", "a9", "a:"},
		{"az", "az", "a{"},
		{"AZ", "az", "a{"},
	}
	for _, tt := range tests {
		lower, upper, err := usernamePrefixRange(tt.prefix)
		require.NoError(t, err, tt.prefix)
		require.Equal(t, tt.lower, lower, tt.prefix)
		require.Equal(t, tt.upper, upper, tt.prefix)
	}

	// Only non-empty ASCII prefixes, as produced by the query validator, have
	// a bound computed by incrementing the last byte.
	for _, prefix := range []string{"", "a\x7f", "a\xff", "alисa"} {
		_, _, err := usernamePrefixRange(prefix)
		require.ErrorIs(t, err, errUnsupportedPrefix, prefix)
	}
}

// FuzzUsernamePrefixRange verifies that under byte order (COLLATE "C") the
// range [lower, upper) contains every string that starts with the lowercased
// prefix and nothing that does not.
func FuzzUsernamePrefixRange(f *testing.F) {
	for _, seed := range []struct{ prefix, name string }{
		{"bo", "bob"}, {"Al_", "al_x"}, {"a-", "a."}, {"az", "a{"}, {"a9", "a9\xff"}, {"", "a"}, {"a\x7f", "a"},
	} {
		f.Add(seed.prefix, seed.name)
	}
	f.Fuzz(func(t *testing.T, prefix, name string) {
		lower, upper, err := usernamePrefixRange(prefix)
		if err != nil {
			return
		}
		require.Equal(t, strings.ToLower(prefix), lower)
		inRange := lower <= name && name < upper
		require.Equal(t, strings.HasPrefix(name, lower), inRange, "name %q, range [%q, %q)", name, lower, upper)
	})
}
