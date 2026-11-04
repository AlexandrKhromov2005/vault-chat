package middleware_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
)

var subjectKey = regexp.MustCompile(`^ip:203\.0\.113\.7:(-|[0-9a-f]{64})$`)

// captureSubject returns the subject key and the body the next handler saw,
// or the read error the unreadable handler saw.
func captureSubject(t *testing.T, body string, limit int64) (key, replayed string, readErr error) {
	t.Helper()
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		key = middleware.ByCredentialSubject(r)
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		replayed = string(b)
	})
	unreadable := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
		require.Error(t, readErr)
	})
	h := middleware.CaptureCredentialSubject(unreadable)(next)
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.RemoteAddr = "203.0.113.7:5555"
	w := httptest.NewRecorder()
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	h.ServeHTTP(w, r)
	return key, replayed, readErr
}

func TestCaptureCredentialSubject(t *testing.T) {
	subject := func(body string) string {
		t.Helper()
		key, replayed, err := captureSubject(t, body, 1<<10)
		require.NoError(t, err)
		require.Equal(t, body, replayed, "the handler reads the same body")
		require.Regexp(t, subjectKey, key)
		return key
	}

	alice := subject(`{"email":"alice@example.test","password":"a"}`)
	require.Equal(t, alice, subject(`{"email":"ALICE@Example.TEST","password":"b"}`), "emails are case-insensitive")
	require.Equal(t, alice, subject(`{"EMAIL":"alice@example.test"}`), "keys match as in encoding/json")
	require.Equal(t, alice, subject(`{"email":"bob@example.test","email":"alice@example.test"}`), "the last duplicate wins, as in the handler")
	require.NotEqual(t, alice, subject(`{"email":"bob@example.test"}`))
	require.NotEqual(t, alice, subject(`{"token":"alice@example.test"}`), "a proof never shares an email's bucket")

	none := "ip:203.0.113.7:-"
	for _, body := range []string{``, `!`, `[]`, `{}`, `{"email":""}`, `{"email":1}`, `{"password":"p"}`, `{"email":"a"} {}`,
		`{"new_email":"alice@example.test"}`} {
		require.Equal(t, none, subject(body), body)
	}

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "203.0.113.7:5555"
	require.Equal(t, none, middleware.ByCredentialSubject(r), "requests that were not captured share one key")

	authenticated := func(userID, body string) string {
		t.Helper()
		var key string
		h := middleware.CaptureCredentialSubject(nil)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			key = middleware.ByCredentialSubject(r)
		}))
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.RemoteAddr = "203.0.113.7:5555"
		r = r.WithContext(middleware.WithIdentity(r.Context(), middleware.Identity{UserID: userID}))
		h.ServeHTTP(httptest.NewRecorder(), r)
		require.Regexp(t, subjectKey, key)
		return key
	}
	own := authenticated("u-1", `{"new_email":"a@example.test"}`)
	require.Equal(t, own, authenticated("u-1", `{"new_email":"b@example.test"}`), "the account is the subject")
	require.NotEqual(t, own, authenticated("u-2", `{"new_email":"a@example.test"}`))
	require.NotEqual(t, own, subject(`{"new_email":"a@example.test"}`))
}

func TestCaptureCredentialSubjectHandsUnreadableBodiesToTheHandler(t *testing.T) {
	_, _, err := captureSubject(t, `{"email":"`+strings.Repeat("a", 64)+`"}`, 16)
	var tooLarge *http.MaxBytesError
	require.ErrorAs(t, err, &tooLarge, "the handler still answers 413")
}

func FuzzCaptureCredentialSubject(f *testing.F) {
	for _, seed := range []string{`{"email":"a@example.test"}`, `{"new_email":"A@b"}`, `{"token":"t"}`, `{"Email":1}`, `!`, ``} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		key, replayed, err := captureSubject(t, body, 1<<20)
		if err != nil {
			t.Fatal(errors.Join(errors.New("read failed"), err))
		}
		if replayed != body {
			t.Fatalf("body %q replayed as %q", body, replayed)
		}
		if !subjectKey.MatchString(key) {
			t.Fatalf("subject key %q", key)
		}
	})
}
