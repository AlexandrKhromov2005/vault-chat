package requestid_test

import (
	"testing"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/requestid"
)

func FuzzValid(f *testing.F) {
	for _, id := range []string{"", "req-1", "3f1c2d4e-5a6b-4c7d-8e9f-0a1b2c3d4e5f", "a b", "x\r\ny", "\xff"} {
		f.Add(id)
	}
	f.Fuzz(func(t *testing.T, id string) {
		if !requestid.Valid(id) {
			return
		}
		// Invariant: an accepted id is safe to echo in a header and a log line.
		if len(id) == 0 || len(id) > 64 {
			t.Fatalf("Valid accepted id of length %d", len(id))
		}
		for i := 0; i < len(id); i++ {
			c := id[i]
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' {
				continue
			}
			t.Fatalf("Valid accepted id %q with byte %q", id, c)
		}
	})
}
