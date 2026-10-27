package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	authhandler "github.com/AlexandrKhromov2005/vault-chat/internal/auth/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/mail"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
	gatewayhandler "github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/router"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/jwt"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/requestid"
)

const emailAPIAuth = "/api/v1/auth"

type emailAPICapture struct {
	mu       sync.Mutex
	messages []mail.Message
}

func (s *emailAPICapture) Send(ctx context.Context, message mail.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, message)
	return nil
}

func (s *emailAPICapture) snapshot() []mail.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]mail.Message(nil), s.messages...)
}

type emailAPIFixture struct {
	t       *testing.T
	ctx     context.Context
	pool    *pgxpool.Pool
	svc     *service.Service
	worker  *mail.Worker
	sender  *emailAPICapture
	gateway *httptest.Server
}

// Notification delivery is captured and Redis uses local miniredis. HTTP, gRPC
// serialization, JWTs, password hashing, SQL transactions, encrypted jobs and
// worker leases are real. No SMTP connection is opened by this scenario.
func newEmailAPI(t *testing.T) *emailAPIFixture {
	t.Helper()
	pool := newTestPool(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	privatePEM, publicPEM := generateRSAKeys(t)
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	keys := t.TempDir()
	require.NoError(t, os.Chmod(keys, 0o700))
	for name, data := range map[string][]byte{"jwt-private.pem": privatePEM, "jwt-public.pem": publicPEM, "mail.key": key} {
		require.NoError(t, os.WriteFile(filepath.Join(keys, name), data, 0o600))
	}
	tokenManager, err := jwt.NewManager(privatePEM, publicPEM, 15*time.Minute, time.Hour)
	require.NoError(t, err)
	box, err := mail.NewBox(key)
	clear(key)
	require.NoError(t, err)
	emails := repository.NewEmailRepository(pool)
	accounts, err := service.NewEmailAccounts(emails, box, "https://vault.example.invalid")
	require.NoError(t, err)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	svc, err := service.NewService(repository.NewUserRepository(pool), repository.NewSessionRepository(pool),
		service.NewArgon2idHasher(service.Argon2idParams{MemoryKiB: 1024, Time: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}),
		tokenManager, logger, service.WithInvitations(repository.NewUserRepository(pool)), service.WithEmailAccounts(accounts))
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	server := grpc.NewServer(grpc.UnaryInterceptor(requestid.UnaryServerInterceptor()))
	authv1.RegisterAuthServiceServer(server, authhandler.NewAuthGRPCHandler(svc))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn := newAuthConn(t, func(ctx context.Context, _ string) (net.Conn, error) {
		return new(net.Dialer).DialContext(ctx, "tcp", listener.Addr().String())
	})
	deps := gatewayDeps(t, conn, miniredis.RunT(t).Addr(), 1000)
	deps.Web = gatewayhandler.NewWebApp(fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>Synthetic email acceptance</title>")}})
	gateway := httptest.NewServer(router.New(deps))
	gateway.Client().Timeout = 10 * time.Second
	t.Cleanup(gateway.Close)
	sender := new(emailAPICapture)
	return &emailAPIFixture{t: t, ctx: ctx, pool: pool, svc: svc, worker: mail.NewWorker(emails, sender, box, logger), sender: sender, gateway: gateway}
}

func (f *emailAPIFixture) request(method, path, access string, body any, want int) map[string]any {
	f.t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		require.NoError(f.t, err)
	}
	req, err := http.NewRequestWithContext(f.ctx, method, f.gateway.URL+path, bytes.NewReader(data))
	require.NoError(f.t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if access != "" {
		req.Header.Set("Authorization", "Bearer "+access)
	}
	resp, err := f.gateway.Client().Do(req)
	require.NoError(f.t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	require.NoError(f.t, err)
	require.Equal(f.t, want, resp.StatusCode, "unexpected HTTP status on %s %s", method, strings.Split(path, "?")[0])
	if method == http.MethodGet && strings.HasPrefix(path, "/#") {
		return nil
	}
	var result map[string]any
	require.NoError(f.t, json.Unmarshal(raw, &result))
	return result
}

func (f *emailAPIFixture) invite(email string) string {
	f.t.Helper()
	var random [32]byte
	_, err := rand.Read(random[:])
	require.NoError(f.t, err)
	token := hex.EncodeToString(random[:])
	digest := sha256.Sum256([]byte(token))
	_, err = f.pool.Exec(f.ctx, `INSERT INTO registration_invitations(token_hash,email,expires_at) VALUES($1,$2,clock_timestamp()+interval '1 hour')`, digest[:], email)
	require.NoError(f.t, err)
	return token
}

func (f *emailAPIFixture) login(email, password string) tokens {
	f.t.Helper()
	body := f.request(http.MethodPost, emailAPIAuth+"/login", "", map[string]string{"email": email, "password": password}, http.StatusOK)
	require.Equal(f.t, "Bearer", body["token_type"])
	return tokens{access: body["access_token"].(string), refresh: body["refresh_token"].(string)}
}

func (f *emailAPIFixture) drain() []mail.Message {
	f.t.Helper()
	start := len(f.sender.snapshot())
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	for range 10 {
		handled, err := f.worker.Step(ctx)
		require.NoError(f.t, err)
		if !handled {
			return f.sender.snapshot()[start:]
		}
	}
	f.t.Fatal("synthetic queue exceeded its expected acceptance budget")
	return nil
}

func emailAPIProof(t *testing.T, message mail.Message, purpose string) string {
	t.Helper()
	for _, word := range strings.Fields(message.Text) {
		link, err := url.Parse(word)
		if err != nil || link.Scheme != "https" || link.Host != "vault.example.invalid" {
			continue
		}
		require.Empty(t, link.RawQuery)
		values, err := url.ParseQuery(link.Fragment)
		require.NoError(t, err)
		require.Len(t, values, 1)
		token := values.Get(purpose)
		require.Len(t, token, 64)
		return token
	}
	t.Fatal("encrypted notification did not contain the expected proof link")
	return ""
}

func (f *emailAPIFixture) noActiveSessions(owner string) {
	f.t.Helper()
	var count int
	require.NoError(f.t, f.pool.QueryRow(f.ctx, `SELECT count(*) FROM sessions WHERE user_id=$1::uuid AND revoked_at IS NULL`, owner).Scan(&count))
	require.Zero(f.t, count, "confirmation must revoke sessions and must not log in automatically")
}

func TestEmailAPIRegistrationRecoveryAndChange(t *testing.T) {
	f := newEmailAPI(t)
	const email, newEmail, password, newPassword = "alice@example.invalid", "alice-new@example.invalid", "Password12", "NewPassword34"
	registered := f.request(http.MethodPost, emailAPIAuth+"/register", "", map[string]string{
		"email": email, "username": "alice", "password": password, "invitation_token": f.invite(email),
	}, http.StatusCreated)
	require.Equal(t, true, registered["verification_required"])
	require.NotContains(t, registered, "access_token")
	require.NotContains(t, registered, "refresh_token")
	owner := registered["user_id"].(string)
	f.noActiveSessions(owner)
	blocked := f.request(http.MethodPost, emailAPIAuth+"/login", "", map[string]string{"email": email, "password": password}, http.StatusForbidden)
	require.Equal(t, "email_verification_required", blocked["error"].(map[string]any)["code"])

	// Public reset responses do not disclose either existence or verification.
	unknown := f.request(http.MethodPost, emailAPIAuth+"/password/reset/request", "", map[string]string{"email": "unknown@example.invalid"}, http.StatusAccepted)
	unverified := f.request(http.MethodPost, emailAPIAuth+"/password/reset/request", "", map[string]string{"email": email}, http.StatusAccepted)
	require.Equal(t, unknown, unverified)
	var queued int
	require.NoError(t, f.pool.QueryRow(f.ctx, `SELECT count(*) FROM email_outbox`).Scan(&queued))
	require.Equal(t, 1, queued, "only the registration ownership message may have been enqueued")
	var ciphertext []byte
	require.NoError(t, f.pool.QueryRow(f.ctx, `SELECT payload FROM email_outbox`).Scan(&ciphertext))
	verification := f.drain()
	require.Len(t, verification, 1)
	require.Equal(t, email, verification[0].To)
	verifyProof := emailAPIProof(t, verification[0], "verify-email")
	require.False(t, bytes.Contains(ciphertext, []byte(verifyProof)))
	require.False(t, bytes.Contains(ciphertext, []byte(email)))
	f.request(http.MethodGet, "/#verify-email="+verifyProof, "", nil, http.StatusOK)
	f.request(http.MethodGet, emailAPIAuth+"/email/verification/confirm?token="+verifyProof, "", nil, http.StatusNotFound)
	var verified bool
	require.NoError(t, f.pool.QueryRow(f.ctx, `SELECT email_verified_at IS NOT NULL FROM users WHERE id=$1::uuid`, owner).Scan(&verified))
	require.False(t, verified, "GET/preview must not consume the proof")
	wrong := f.request(http.MethodPost, emailAPIAuth+"/password/reset/confirm", "", map[string]string{"token": verifyProof, "password": newPassword}, http.StatusBadRequest)
	require.Equal(t, "invalid_email_action", wrong["error"].(map[string]any)["code"])
	confirmed := f.request(http.MethodPost, emailAPIAuth+"/email/verification/confirm", "", map[string]string{"token": verifyProof}, http.StatusOK)
	require.Equal(t, owner, confirmed["user_id"])
	require.Len(t, confirmed, 2)
	f.request(http.MethodPost, emailAPIAuth+"/email/verification/confirm", "", map[string]string{"token": verifyProof}, http.StatusBadRequest)
	old := f.login(email, password)
	state := f.request(http.MethodGet, emailAPIAuth+"/account", old.access, nil, http.StatusOK)
	require.Equal(t, true, state["email_verified"])

	// Bob is an independently owned, verified fixture identity and session.
	bob, err := f.svc.Register(f.ctx, "bob@example.invalid", "bob", password)
	require.NoError(t, err)
	_, err = f.pool.Exec(f.ctx, `UPDATE users SET email_verified_at=clock_timestamp() WHERE id=$1::uuid`, bob.ID)
	require.NoError(t, err)
	bobSession := f.login(bob.Email, password)
	bobAccount := f.request(http.MethodGet, emailAPIAuth+"/account?user_id="+owner, bobSession.access, nil, http.StatusOK)
	require.Equal(t, bob.ID, bobAccount["user_id"])
	require.Equal(t, bob.Email, bobAccount["email"])
	profile := f.request(http.MethodGet, "/api/v1/users/"+owner, bobSession.access, nil, http.StatusOK)
	require.NotContains(t, profile, "email")
	require.Len(t, profile, 2)

	f.request(http.MethodPost, emailAPIAuth+"/password/reset/request", "", map[string]string{"email": email}, http.StatusAccepted)
	recovery := f.drain()
	require.Len(t, recovery, 1)
	resetProof := emailAPIProof(t, recovery[0], "reset-password")
	f.request(http.MethodPost, emailAPIAuth+"/email/change/confirm", "", map[string]string{"token": resetProof}, http.StatusBadRequest)
	reset := f.request(http.MethodPost, emailAPIAuth+"/password/reset/confirm", "", map[string]string{"token": resetProof, "password": newPassword}, http.StatusOK)
	require.Equal(t, owner, reset["user_id"])
	require.Len(t, reset, 2)
	f.noActiveSessions(owner)
	f.request(http.MethodGet, emailAPIAuth+"/account", old.access, nil, http.StatusUnauthorized)
	f.request(http.MethodPost, emailAPIAuth+"/refresh", "", map[string]string{"refresh_token": old.refresh}, http.StatusUnauthorized)
	f.request(http.MethodPost, emailAPIAuth+"/password/reset/confirm", "", map[string]string{"token": resetProof, "password": newPassword}, http.StatusBadRequest)
	notices := f.drain()
	require.Len(t, notices, 1)
	require.Equal(t, email, notices[0].To)
	require.NotContains(t, notices[0].Text, newPassword)
	current := f.login(email, newPassword)

	f.request(http.MethodPost, emailAPIAuth+"/email/change/request", current.access, map[string]string{"password": newPassword, "new_email": newEmail}, http.StatusAccepted)
	changeMail := f.drain()
	require.Len(t, changeMail, 1)
	require.Equal(t, newEmail, changeMail[0].To)
	changeProof := emailAPIProof(t, changeMail[0], "confirm-email-change")
	f.request(http.MethodGet, emailAPIAuth+"/email/change/confirm?token="+changeProof, "", nil, http.StatusNotFound)
	before := f.request(http.MethodGet, emailAPIAuth+"/account", current.access, nil, http.StatusOK)
	require.Equal(t, email, before["email"])
	f.request(http.MethodPost, emailAPIAuth+"/email/verification/confirm", "", map[string]string{"token": changeProof}, http.StatusBadRequest)
	changed := f.request(http.MethodPost, emailAPIAuth+"/email/change/confirm", "", map[string]string{"token": changeProof}, http.StatusOK)
	require.Equal(t, owner, changed["user_id"])
	require.Len(t, changed, 2)
	f.noActiveSessions(owner)
	f.request(http.MethodGet, emailAPIAuth+"/account", current.access, nil, http.StatusUnauthorized)
	f.request(http.MethodPost, emailAPIAuth+"/refresh", "", map[string]string{"refresh_token": current.refresh}, http.StatusUnauthorized)
	f.request(http.MethodPost, emailAPIAuth+"/email/change/confirm", "", map[string]string{"token": changeProof}, http.StatusBadRequest)
	notices = f.drain()
	require.Len(t, notices, 2)
	recipients := []string{notices[0].To, notices[1].To}
	sort.Strings(recipients)
	expected := []string{email, newEmail}
	sort.Strings(expected)
	require.Equal(t, expected, recipients)
	updated := f.login(newEmail, newPassword)
	final := f.request(http.MethodGet, emailAPIAuth+"/account", updated.access, nil, http.StatusOK)
	require.Equal(t, newEmail, final["email"])
	require.Equal(t, true, final["email_verified"])
	f.request(http.MethodPost, emailAPIAuth+"/login", "", map[string]string{"email": email, "password": newPassword}, http.StatusUnauthorized)
	f.request(http.MethodGet, emailAPIAuth+"/account", bobSession.access, nil, http.StatusOK)
	var remaining int
	require.NoError(t, f.pool.QueryRow(f.ctx, `SELECT count(*) FROM email_outbox`).Scan(&remaining))
	require.Zero(t, remaining)
}
