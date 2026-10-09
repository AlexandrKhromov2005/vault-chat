package middleware

import (
	"strings"
	"testing"
)

func FuzzBearerToken(f *testing.F) {
	for _, header := range []string{
		"", "Bearer tok", "bearer tok", "BEARER a.b.c", "Basic abc", "Bearer ", "Bearer  tok",
		"Bearertok", "Bearer tok extra", "Bearer tok\x00", "Bearer \t",
	} {
		f.Add(header)
	}
	f.Fuzz(func(t *testing.T, header string) {
		token, ok := bearerToken(header)
		if !ok {
			if token != "" {
				t.Fatalf("bearerToken(%q) rejected the header but returned token %q", header, token)
			}
			return
		}
		// Invariants: the token is a non-empty run of visible ASCII taken
		// verbatim from the end of a header that starts with the Bearer scheme.
		if token == "" {
			t.Fatalf("bearerToken(%q) accepted an empty token", header)
		}
		for i := 0; i < len(token); i++ {
			if token[i] <= ' ' || token[i] >= 0x7f {
				t.Fatalf("bearerToken(%q) accepted token with byte %q", header, token[i])
			}
		}
		if !strings.HasSuffix(header, token) || !strings.EqualFold(header[:len("Bearer ")], "Bearer ") {
			t.Fatalf("bearerToken(%q) = %q is not the Bearer credential", header, token)
		}
	})
}
