package push

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// writeAccount writes a synthetic service account with a fresh key; no real
// Firebase credentials exist in the repository.
func writeAccount(t *testing.T, mutate func(map[string]string)) (string, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	account := map[string]string{
		"type": "service_account", "project_id": "synthetic-project", "client_email": "sender@synthetic-project.invalid",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "token_uri": fcmTokenURI,
	}
	if mutate != nil {
		mutate(account)
	}
	data, err := json.Marshal(account)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "account.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path, key
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

type fakeGoogle struct {
	key       *rsa.PrivateKey
	exchanges atomic.Int32
	answer    func(w http.ResponseWriter, body []byte)
	lastBody  atomic.Value
}

func (g *fakeGoogle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	switch r.URL.Path {
	case "/token":
		g.exchanges.Add(1)
		form, _ := url.ParseQuery(string(body))
		if form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			http.Error(w, "bad grant", http.StatusBadRequest)
			return
		}
		parts := strings.Split(form.Get("assertion"), ".")
		if len(parts) != 3 {
			http.Error(w, "bad assertion", http.StatusBadRequest)
			return
		}
		signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if rsa.VerifyPKCS1v15(&g.key.PublicKey, crypto.SHA256, digest[:], signature) != nil {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var c map[string]any
		_ = json.Unmarshal(claims, &c)
		if c["scope"] != fcmScope || c["aud"] != fcmTokenURI || c["iss"] != "sender@synthetic-project.invalid" {
			http.Error(w, "bad claims", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"synthetic-access","expires_in":3600}`))
	case "/v1/projects/synthetic-project/messages:send":
		if r.Header.Get("Authorization") != "Bearer synthetic-access" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		g.lastBody.Store(body)
		g.answer(w, body)
	default:
		http.NotFound(w, r)
	}
}

func newTestFCM(t *testing.T, answer func(http.ResponseWriter, []byte)) (*FCM, *fakeGoogle) {
	t.Helper()
	path, key := writeAccount(t, nil)
	f, err := NewFCM(readFile(t, path))
	require.NoError(t, err)
	google := &fakeGoogle{key: key, answer: answer}
	server := httptest.NewServer(google)
	t.Cleanup(server.Close)
	f.tokenURI, f.endpoint = server.URL+"/token", server.URL
	return f, google
}

func TestFCMSendsADataOnlySignal(t *testing.T) {
	f, google := newTestFCM(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(`{"name":"projects/p/messages/1"}`)) })
	ctx := context.Background()
	require.NoError(t, f.Send(ctx, Message{Token: "device-token", RoomID: "room-1"}))
	require.NoError(t, f.Send(ctx, Message{Token: "device-token", RoomID: "room-1"}))
	require.EqualValues(t, 1, google.exchanges.Load(), "the access token is cached")

	var sent map[string]any
	require.NoError(t, json.Unmarshal(google.lastBody.Load().([]byte), &sent))
	require.Equal(t, map[string]any{"message": map[string]any{
		"token":   "device-token",
		"data":    map[string]any{"type": "sync", "room_id": "room-1"},
		"android": map[string]any{"priority": "HIGH", "collapse_key": "room:room-1", "ttl": "3600s"},
	}}, sent, "no notification text, names or sender")

	f.now = func() time.Time { return time.Now().Add(59 * time.Minute) }
	require.NoError(t, f.Send(ctx, Message{Token: "device-token", RoomID: "room-1"}))
	require.EqualValues(t, 2, google.exchanges.Load(), "an access token near expiry is renewed")
}

func TestFCMMapsProviderAnswers(t *testing.T) {
	for name, tc := range map[string]struct {
		status  int
		body    string
		invalid bool
	}{
		"unregistered": {http.StatusNotFound, `{"error":{"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`, true},
		// A service account of another project is a configuration error: the
		// tokens are still valid for the app's own project.
		"sender mismatch": {http.StatusForbidden, `{"error":{"status":"PERMISSION_DENIED","details":[{"errorCode":"SENDER_ID_MISMATCH"}]}}`, false},
		// A proxy or wrong endpoint answering 404 must not delete every token.
		"plain not found":    {http.StatusNotFound, `not found`, false},
		"404 without code":   {http.StatusNotFound, `{"error":{"status":"NOT_FOUND"}}`, false},
		"invalid argument":   {http.StatusBadRequest, `{"error":{"status":"INVALID_ARGUMENT"}}`, false},
		"quota":              {http.StatusTooManyRequests, `{"error":{"status":"RESOURCE_EXHAUSTED"}}`, false},
		"unavailable":        {http.StatusServiceUnavailable, `not json`, false},
		"hostile error code": {http.StatusInternalServerError, `{"error":{"status":"device-token <script>"}}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			f, _ := newTestFCM(t, func(w http.ResponseWriter, _ []byte) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			err := f.Send(context.Background(), Message{Token: "device-token", RoomID: "room-1"})
			require.Error(t, err)
			require.Equal(t, tc.invalid, errors.Is(err, ErrTokenInvalid))
			require.NotContains(t, err.Error(), "device-token", "errors never carry the token")
		})
	}
}

func TestFCMRejectsUnusableCredentials(t *testing.T) {
	for name, mutate := range map[string]func(map[string]string){
		"foreign token uri": func(a map[string]string) { a["token_uri"] = "https://attacker.invalid/token" },
		"not a service":     func(a map[string]string) { a["type"] = "authorized_user" },
		"bad project":       func(a map[string]string) { a["project_id"] = "../x" },
		"no email":          func(a map[string]string) { a["client_email"] = "" },
		"no key":            func(a map[string]string) { a["private_key"] = "" },
	} {
		t.Run(name, func(t *testing.T) {
			path, _ := writeAccount(t, mutate)
			_, err := NewFCM(readFile(t, path))
			require.Error(t, err)
			require.NotContains(t, err.Error(), "PRIVATE KEY")
		})
	}
	_, err := NewFCM(make([]byte, MaxCredentialsBytes+1))
	require.Error(t, err)
	_, err = NewFCM([]byte("!"))
	require.Error(t, err)
}

func TestFCMTokenExchangeFailureIsAnError(t *testing.T) {
	f, _ := newTestFCM(t, func(http.ResponseWriter, []byte) {})
	_, other := writeAccount(t, nil)
	f.key = other // Google rejects a signature by the wrong key.
	err := f.Send(context.Background(), Message{Token: "device-token", RoomID: "room-1"})
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrTokenInvalid), "an auth failure must not delete tokens")
}
