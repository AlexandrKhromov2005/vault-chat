package validator

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidatePushToken(t *testing.T) {
	require.NoError(t, ValidatePushToken("fcm", "dGVzdA:APA91b-synthetic_token.1"))
	require.NoError(t, ValidatePushToken("fcm", strings.Repeat("a", MaxPushTokenLength)))
	for _, tc := range []struct{ provider, token string }{
		{"fcm", ""},
		{"fcm", strings.Repeat("a", MaxPushTokenLength+1)},
		{"fcm", "token with space"},
		{"fcm", "token\n"},
		{"fcm", "токен"},
		{"fcm", "token/../x"},
		{"FCM", "token"},
		{"apns", "token"},
		{"", "token"},
	} {
		require.ErrorIs(t, ValidatePushToken(tc.provider, tc.token), ErrInvalidPushToken, "%q %q", tc.provider, tc.token)
	}
}

func pushTokenChar(c rune) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("_:.-", c)
}

func FuzzValidatePushToken(f *testing.F) {
	for _, seed := range []string{"token", "", "a b", "a:b.c-d_e", "\x00", "токен"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, token string) {
		if ValidatePushToken("fcm", token) != nil {
			return
		}
		if token == "" || len(token) > MaxPushTokenLength {
			t.Fatalf("accepted token of length %d", len(token))
		}
		for _, c := range token {
			if !pushTokenChar(c) {
				t.Fatalf("accepted %q in %q", c, token)
			}
		}
	})
}
