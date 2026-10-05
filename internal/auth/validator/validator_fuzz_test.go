package validator_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
)

// FuzzValidateEmail verifies that validation never panics, is deterministic,
// and that accepted values satisfy the documented email invariants.
func FuzzValidateEmail(f *testing.F) {
	f.Add("user@example.com")
	f.Add("a@b.co")
	f.Add("test.user+tag@domain.org")
	f.Add("")
	f.Add("no-at")
	f.Add("two@@example.com")
	f.Add(strings.Repeat("a", 300))

	f.Fuzz(func(t *testing.T, email string) {
		if len(email) > 4096 {
			t.Skip("input too large")
		}

		err1 := validator.ValidateEmail(email)
		err2 := validator.ValidateEmail(email)
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("non-deterministic validation for %q", email)
		}

		if err1 == nil {
			if strings.Count(email, "@") != 1 {
				t.Fatalf("accepted email without exactly one @: %q", email)
			}
			if len(email) > 254 {
				t.Fatalf("accepted email longer than 254 chars: %d", len(email))
			}
			if !utf8.ValidString(email) {
				t.Fatalf("accepted invalid utf-8 email: %q", email)
			}
		}
	})
}

// FuzzValidateUsername verifies determinism and the documented length
// invariant for accepted usernames.
func FuzzValidateUsername(f *testing.F) {
	f.Add("abc")
	f.Add("a_b-c1")
	f.Add("1abc")
	f.Add("")
	f.Add("a b")
	f.Add(strings.Repeat("x", 64))

	f.Fuzz(func(t *testing.T, username string) {
		if len(username) > 4096 {
			t.Skip("input too large")
		}

		err1 := validator.ValidateUsername(username)
		err2 := validator.ValidateUsername(username)
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("non-deterministic validation for %q", username)
		}

		if err1 == nil {
			if len(username) < 3 || len(username) > 32 {
				t.Fatalf("accepted username of length %d: %q", len(username), username)
			}
		}
	})
}

// FuzzValidatePassword verifies determinism and the documented length
// invariant for accepted passwords.
func FuzzValidatePassword(f *testing.F) {
	f.Add("password1")
	f.Add("abcd1234")
	f.Add("")
	f.Add("short1")
	f.Add("onlyletters")
	f.Add("12345678")
	f.Add(strings.Repeat("a1", 100))

	f.Fuzz(func(t *testing.T, password string) {
		if len(password) > 4096 {
			t.Skip("input too large")
		}

		err1 := validator.ValidatePassword(password)
		err2 := validator.ValidatePassword(password)
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("non-deterministic validation for %q", password)
		}

		if err1 == nil {
			if len(password) < 8 || len(password) > 128 {
				t.Fatalf("accepted password of length %d", len(password))
			}
		}
	})
}
