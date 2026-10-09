package integration

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/ratelimit"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/router"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/requestid"
)

// startAuth serves the real auth stack (PostgreSQL included, see
// newAuthHandler) over an in-memory gRPC connection and records the request
// ids it receives.
func startAuth(t *testing.T) (*grpc.ClientConn, func() []string) {
	t.Helper()

	var mu sync.Mutex
	var requestIDs []string
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer(grpc.UnaryInterceptor(
		func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
			md, _ := metadata.FromIncomingContext(ctx)
			mu.Lock()
			requestIDs = append(requestIDs, md.Get(requestid.MetadataKey)...)
			mu.Unlock()
			return next(ctx, req)
		}))
	authv1.RegisterAuthServiceServer(server, newAuthHandler(t))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn := newAuthConn(t, func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.DialContext(ctx)
	})
	return conn, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), requestIDs...)
	}
}

func newAuthConn(t *testing.T, dialer func(context.Context, string) (net.Conn, error)) *grpc.ClientConn {
	t.Helper()

	conn, err := grpc.NewClient("passthrough:///auth",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(requestid.UnaryClientInterceptor()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func startGateway(t *testing.T, conn *grpc.ClientConn, burst uint32) *httptest.Server {
	t.Helper()

	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })
	limiter, err := ratelimit.NewRedisLimiter(redisClient, 600, burst)
	require.NoError(t, err)

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	authClient := authv1.NewAuthServiceClient(conn)
	gateway := httptest.NewServer(router.New(router.Deps{
		Auth:           handler.NewAuthHandler(authClient, logger),
		Health:         handler.NewHealth(nil, logger),
		TokenValidator: authClient,
		Limiter:        limiter,
		Logger:         logger,
		MaxBodyBytes:   1 << 20,
		RequestTimeout: 5 * time.Second,
	}))
	t.Cleanup(gateway.Close)
	return gateway
}

// call sends a request to the gateway and decodes the JSON response body, if
// there is one.
func call(t *testing.T, method, url, token, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var decoded map[string]any
	if len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, &decoded), string(raw))
	}
	return resp.StatusCode, decoded
}

type tokens struct{ access, refresh string }

// testPassword is the password of every user registered by gateway tests.
const testPassword = "password1"

func login(t *testing.T, api, email string) tokens {
	t.Helper()
	code, body := call(t, http.MethodPost, api+"/login", "",
		`{"email":"`+email+`","password":"`+testPassword+`"}`)
	require.Equal(t, http.StatusOK, code, body)
	require.Equal(t, "Bearer", body["token_type"])
	return tokens{access: body["access_token"].(string), refresh: body["refresh_token"].(string)}
}

func refresh(t *testing.T, api, refreshToken string) (int, tokens) {
	t.Helper()
	code, body := call(t, http.MethodPost, api+"/refresh", "", `{"refresh_token":"`+refreshToken+`"}`)
	if code != http.StatusOK {
		return code, tokens{}
	}
	return code, tokens{access: body["access_token"].(string), refresh: body["refresh_token"].(string)}
}

func TestGateway_AuthFlow(t *testing.T) {
	conn, requestIDs := startAuth(t)
	api := startGateway(t, conn, 1000).URL + "/api/v1/auth"

	code, body := call(t, http.MethodPost, api+"/register", "",
		`{"email":"alice@example.com","username":"alice","password":"password1"}`)
	require.Equal(t, http.StatusCreated, code, body)
	userID := body["user_id"]
	require.NotEmpty(t, userID)

	code, body = call(t, http.MethodPost, api+"/register", "",
		`{"email":"ALICE@example.com","username":"alice2","password":"password1"}`)
	require.Equal(t, http.StatusConflict, code, body)
	require.Equal(t, "email already registered", body["error"].(map[string]any)["message"])

	code, body = call(t, http.MethodPost, api+"/register", "",
		`{"email":"not-an-email","username":"bob","password":"password1"}`)
	require.Equal(t, http.StatusBadRequest, code, body)

	code, body = call(t, http.MethodPost, api+"/login", "", `{"email":"alice@example.com","password":"wrong-pass1"}`)
	require.Equal(t, http.StatusUnauthorized, code, body)

	session := login(t, api, "alice@example.com")

	code, body = call(t, http.MethodGet, api+"/me", session.access, "")
	require.Equal(t, http.StatusOK, code, body)
	require.Equal(t, userID, body["user_id"])
	require.Equal(t, "alice", body["username"])

	// A refresh token must not work as an access token, nor a tampered one.
	code, _ = call(t, http.MethodGet, api+"/me", session.refresh, "")
	require.Equal(t, http.StatusUnauthorized, code)
	code, _ = call(t, http.MethodGet, api+"/me", session.access+"x", "")
	require.Equal(t, http.StatusUnauthorized, code)

	// Every backend call carried the gateway's correlation id.
	require.NotEmpty(t, requestIDs())
	for _, id := range requestIDs() {
		require.True(t, requestid.Valid(id), id)
	}
}

func TestGateway_SessionLifecycle(t *testing.T) {
	conn, _ := startAuth(t)
	api := startGateway(t, conn, 1000).URL + "/api/v1/auth"

	code, body := call(t, http.MethodPost, api+"/register", "",
		`{"email":"bob@example.com","username":"bob","password":"password1"}`)
	require.Equal(t, http.StatusCreated, code, body)

	t.Run("refresh rotates, reuse revokes everything", func(t *testing.T) {
		first := login(t, api, "bob@example.com")

		code, rotated := refresh(t, api, first.refresh)
		require.Equal(t, http.StatusOK, code)
		require.NotEqual(t, first.refresh, rotated.refresh)
		code, _ = call(t, http.MethodGet, api+"/me", rotated.access, "")
		require.Equal(t, http.StatusOK, code)

		// Presenting the rotated-out token again means it leaked.
		code, _ = refresh(t, api, first.refresh)
		require.Equal(t, http.StatusUnauthorized, code)
		code, _ = refresh(t, api, rotated.refresh)
		require.Equal(t, http.StatusUnauthorized, code)
	})

	t.Run("logout ends the session and is idempotent", func(t *testing.T) {
		session := login(t, api, "bob@example.com")

		code, body := call(t, http.MethodPost, api+"/logout", "", `{"refresh_token":"`+session.refresh+`"}`)
		require.Equal(t, http.StatusNoContent, code, body)
		code, _ = refresh(t, api, session.refresh)
		require.Equal(t, http.StatusUnauthorized, code)

		code, _ = call(t, http.MethodPost, api+"/logout", "", `{"refresh_token":"`+session.refresh+`"}`)
		require.Equal(t, http.StatusNoContent, code)

		code, _ = call(t, http.MethodPost, api+"/logout", "", `{"refresh_token":"garbage"}`)
		require.Equal(t, http.StatusUnauthorized, code)
	})

	t.Run("revoke all sessions", func(t *testing.T) {
		laptop := login(t, api, "bob@example.com")
		phone := login(t, api, "bob@example.com")

		code, _ := call(t, http.MethodDelete, api+"/sessions", "", "")
		require.Equal(t, http.StatusUnauthorized, code)

		code, body := call(t, http.MethodDelete, api+"/sessions", laptop.access, "")
		require.Equal(t, http.StatusOK, code, body)
		require.EqualValues(t, 2, body["revoked_count"])

		code, _ = refresh(t, api, laptop.refresh)
		require.Equal(t, http.StatusUnauthorized, code)
		code, _ = refresh(t, api, phone.refresh)
		require.Equal(t, http.StatusUnauthorized, code)
	})
}

func TestGateway_RateLimit(t *testing.T) {
	// Requests without a token are rejected before reaching auth, so no
	// backend is needed.
	conn := newAuthConn(t, func(context.Context, string) (net.Conn, error) {
		return nil, net.ErrClosed
	})
	gateway := startGateway(t, conn, 2)

	for i := 0; i < 2; i++ {
		code, body := call(t, http.MethodGet, gateway.URL+"/api/v1/auth/me", "", "")
		require.Equal(t, http.StatusUnauthorized, code, body)
	}

	resp, err := http.Get(gateway.URL + "/api/v1/auth/me")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.NotEmpty(t, resp.Header.Get("Retry-After"))
}
