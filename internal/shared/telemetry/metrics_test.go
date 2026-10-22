package telemetry_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/telemetry"
)

func scrape(t *testing.T, m *telemetry.Metrics) string {
	t.Helper()
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	require.Equal(t, 200, w.Code)
	return w.Body.String()
}

func TestHTTPBoundsLabelsAndCountsErrors(t *testing.T) {
	m := telemetry.New("gateway")
	h := m.HTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("ODD-METHOD", "/api/v1/rooms/private-user-id?token=secret", nil))
	body := scrape(t, m)
	require.Contains(t, body, `vault_http_requests_total{kind="api",method="OTHER",status="503"} 1`)
	require.Contains(t, body, "vault_http_request_duration_seconds_count")
	require.NotContains(t, body, "private-user-id")
	require.NotContains(t, body, "secret")
	// Per-instance registries can coexist without duplicate registration.
	require.NotEmpty(t, scrape(t, telemetry.New("auth")))
}

func TestGRPCCountsStatusWithoutRequestData(t *testing.T) {
	m := telemetry.New("auth")
	_, err := m.Unary()(context.Background(), "password", &grpc.UnaryServerInfo{FullMethod: "/auth.v1.AuthService/Login"}, func(context.Context, any) (any, error) {
		return nil, status.Error(codes.Unauthenticated, "private message")
	})
	require.Error(t, err)
	body := scrape(t, m)
	require.Contains(t, body, `vault_grpc_requests_total{code="Unauthenticated",method="/auth.v1.AuthService/Login"} 1`)
	require.NotContains(t, body, "password")
	require.NotContains(t, body, "private message")
}

func TestHTTPPreservesHijackingAndExcludesWebSocketLifetime(t *testing.T) {
	m := telemetry.New("gateway")
	server := httptest.NewServer(m.HTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, rw, err := http.NewResponseController(w).Hijack()
		require.NoError(t, err)
		defer conn.Close()
		_, err = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		require.NoError(t, err)
		require.NoError(t, rw.Flush())
	})))
	defer server.Close()
	req, err := http.NewRequest("GET", server.URL+"/api/v1/ws", nil)
	require.NoError(t, err)
	conn, err := http.DefaultTransport.(*http.Transport).DialContext(context.Background(), "tcp", strings.TrimPrefix(server.URL, "http://"))
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, req.Write(conn))
	response, err := http.ReadResponse(bufio.NewReader(conn), req)
	require.NoError(t, err)
	require.Equal(t, 101, response.StatusCode)
	require.NotContains(t, scrape(t, m), `vault_http_request_duration_seconds_count{kind="websocket"`)
}
