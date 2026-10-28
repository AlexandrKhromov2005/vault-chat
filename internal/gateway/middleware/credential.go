package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// noSubject is the subject of credential requests whose body names none.
const noSubject = "-"

type subjectKey struct{}

// CaptureCredentialSubject reads what a credential request is about (an email
// or an email proof) from its body, then replays the body. Behind
// Authenticate the subject is the caller's account instead. A body that cannot
// be read in full, because it is too large or late, goes with its read error
// straight to unreadable, the route's handler: it answers 413 or 408 without
// a backend call, so later limits have nothing to protect.
func CaptureCredentialSubject(unreadable http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				r.Body = readCloser{io.MultiReader(bytes.NewReader(body), errReader{err}), r.Body}
				unreadable.ServeHTTP(w, r)
				return
			}
			r.Body = readCloser{bytes.NewReader(body), r.Body}
			subject := credentialSubject(body)
			if identity, ok := IdentityFromContext(r.Context()); ok {
				// An authenticated request checks the caller's own password,
				// so the account is the subject, whatever the body names.
				subject = hashSubject("user:" + identity.UserID)
			}
			ctx := context.WithValue(r.Context(), subjectKey{}, subject)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// credentialSubject hashes the field Auth identifies the request by, with
// the email case-insensitive as in Auth. Field matching follows encoding/json
// as the handlers do, so a body cannot name one subject here and another
// there.
func credentialSubject(body []byte) string {
	var fields struct {
		Email string `json:"email"`
		Token string `json:"token"`
	}
	if json.Unmarshal(body, &fields) != nil {
		return noSubject
	}
	var subject string
	switch {
	case fields.Email != "":
		subject = "email:" + strings.ToLower(fields.Email)
	case fields.Token != "":
		subject = "token:" + fields.Token
	default:
		return noSubject
	}
	return hashSubject(subject)
}

func hashSubject(subject string) string {
	sum := sha256.Sum256([]byte(subject))
	return hex.EncodeToString(sum[:])
}

// ByCredentialSubject counts credential requests per client IP and subject.
// Place it after CaptureCredentialSubject.
func ByCredentialSubject(r *http.Request) string {
	subject, ok := r.Context().Value(subjectKey{}).(string)
	if !ok {
		subject = noSubject
	}
	return ByClientIP(r) + ":" + subject
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

type readCloser struct {
	io.Reader
	io.Closer
}
