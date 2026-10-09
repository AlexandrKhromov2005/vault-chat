package config

import (
	"net/url"
	"strings"
	"testing"
)

func FuzzParseOrigins(f *testing.F) {
	for _, value := range []string{
		"", "*", "https://chat.example.com", "http://localhost:3000, https://a.b",
		"https://chat.example.com/", "chat.example.com", "*,https://a.b", " , ,", "https://[::1]:8080",
	} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, value string) {
		origins, err := parseOrigins(value)
		if err != nil {
			return
		}
		for _, origin := range origins {
			if origin != strings.TrimSpace(origin) || origin == "" {
				t.Fatalf("parseOrigins(%q) returned untrimmed or empty origin %q", value, origin)
			}
			if origin == "*" {
				if len(origins) != 1 {
					t.Fatalf("parseOrigins(%q) mixed wildcard with explicit origins: %q", value, origins)
				}
				continue
			}
			// Invariant: every accepted origin is exactly scheme://host[:port],
			// the form browsers send in the Origin header.
			parsed, parseErr := url.Parse(origin)
			if parseErr != nil {
				t.Fatalf("parseOrigins(%q) accepted unparsable origin %q: %v", value, origin, parseErr)
			}
			if parsed.Scheme+"://"+parsed.Host != origin {
				t.Fatalf("parseOrigins(%q) accepted non-canonical origin %q", value, origin)
			}
		}
	})
}
