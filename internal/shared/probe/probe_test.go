package probe_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/probe"
)

func TestHTTP(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"ok", http.StatusOK, false},
		{"no content", http.StatusNoContent, false},
		{"unavailable", http.StatusServiceUnavailable, true},
		{"redirect is not healthy", http.StatusFound, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/health", r.URL.Path)
				if tt.status == http.StatusFound {
					w.Header().Set("Location", "/elsewhere")
				}
				w.WriteHeader(tt.status)
			}))
			t.Cleanup(server.Close)

			err := probe.HTTP(context.Background(), server.URL+"/health")
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestHTTPUnreachable(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()

	require.Error(t, probe.HTTP(context.Background(), url))
}

func TestHTTPInvalidURL(t *testing.T) {
	require.Error(t, probe.HTTP(context.Background(), "://bad"))
}

func TestHTTPRespectsDeadline(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, probe.HTTP(ctx, server.URL), context.DeadlineExceeded)
}

func TestGRPC(t *testing.T) {
	tests := []struct {
		name    string
		status  healthpb.HealthCheckResponse_ServingStatus
		wantErr bool
	}{
		{"serving", healthpb.HealthCheckResponse_SERVING, false},
		{"not serving", healthpb.HealthCheckResponse_NOT_SERVING, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			healthServer := health.NewServer()
			healthServer.SetServingStatus("", tt.status)
			addr := startGRPC(t, healthServer)

			err := probe.GRPC(context.Background(), addr)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestGRPCUnreachable(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.Error(t, probe.GRPC(ctx, addr))
}

func TestGRPCWithoutHealthService(t *testing.T) {
	addr := startGRPC(t, nil)

	require.Error(t, probe.GRPC(context.Background(), addr))
}

func startGRPC(t *testing.T, healthServer *health.Server) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	if healthServer != nil {
		healthpb.RegisterHealthServer(server, healthServer)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}
