package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func FuzzDecodeJSON(f *testing.F) {
	for _, body := range []string{
		``, `{}`, `null`, `[]`, `{"email":"a@b.co","password":"x"}`, `{"email":1}`, `{"emial":"x"}`,
		`{} {}`, `{"email":"\u0000"}`, `{"email":"a"`, "\xff",
	} {
		f.Add(body)
	}
	f.Fuzz(func(t *testing.T, body string) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Body = http.MaxBytesReader(httptest.NewRecorder(), req.Body, 1024)

		var dst loginRequest
		err := decodeJSON(req, &dst)
		if err != nil {
			if err.status < 400 || err.status > 499 || err.code == "" || err.message == "" {
				t.Fatalf("decodeJSON(%q) returned a malformed error %+v", body, err)
			}
			return
		}
		// Invariant: an accepted body is a single valid JSON value that fits
		// the limit.
		if len(body) > 1024 || !json.Valid([]byte(body)) {
			t.Fatalf("decodeJSON accepted invalid body %q", body)
		}
	})
}
