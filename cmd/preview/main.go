// Command preview runs one isolated image decoder task at a time.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	previewv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/preview/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/media/preview"
	sharedlogger "github.com/AlexandrKhromov2005/vault-chat/internal/shared/logger"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/requestid"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/rpctls"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/telemetry"
)

func main() {
	if err := run(); err != nil {
		slog.Error("preview failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 {
		switch {
		case len(os.Args) == 3 && os.Args[1] == "-health":
			return checkHealth(os.Args[2])
		case len(os.Args) == 3 && os.Args[1] == "-check":
			return checkDecode(os.Args[2], "")
		case len(os.Args) == 5 && os.Args[1] == "-check" && os.Args[3] == "-image":
			return checkDecode(os.Args[2], os.Args[4])
		default:
			return errors.New("usage: preview | preview -health ADDR | preview -check ADDR [-image PATH]")
		}
	}
	return serve()
}

func serve() error {
	decoderPath := os.Getenv("PREVIEW_DECODER_PATH")
	if decoderPath == "" {
		return errors.New("PREVIEW_DECODER_PATH is required")
	}
	addr := os.Getenv("PREVIEW_GRPC_ADDR")
	if addr == "" {
		addr = ":50054"
	}
	logger := sharedlogger.New(os.Getenv("LOG_LEVEL"))
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	metrics := telemetry.New("preview")
	stopMetrics, err := metrics.Listen(ctx, os.Getenv("PREVIEW_METRICS_ADDR"))
	if err != nil {
		return fmt.Errorf("listen metrics: %w", err)
	}
	defer stopMetrics()
	rpcTLS, err := rpctls.Load("preview")
	if err != nil {
		return fmt.Errorf("configure RPC TLS: %w", err)
	}
	metrics.Gauge("vault_rpc_mtls_enabled", "Whether internal RPC requires mutual TLS.", func() float64 {
		if rpcTLS.Enabled() {
			return 1
		}
		return 0
	})
	metrics.Gauge("vault_rpc_certificate_not_after_timestamp_seconds", "Loaded internal certificate expiry; zero if mTLS is off.", rpcTLS.CertificateExpiry)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := preview.NewRPCServer(
		rpcTLS.ServerOption(),
		grpc.ChainUnaryInterceptor(metrics.Unary(), rpcTLS.Unary(), requestid.UnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(rpcTLS.Stream()),
	)
	previewv1.RegisterPreviewServiceServer(server, preview.NewRPCHandler(&preview.Worker{Path: decoderPath, Timeout: 5 * time.Second}))
	hs := health.NewServer()
	healthpb.RegisterHealthServer(server, hs)
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	logger.Info("preview listening", "addr", addr)
	select {
	case err := <-serveErr:
		if !errors.Is(err, grpc.ErrServerStopped) {
			return fmt.Errorf("serve preview: %w", err)
		}
		return nil
	case <-ctx.Done():
	}
	hs.Shutdown()
	done := make(chan struct{})
	go func() { server.GracefulStop(); close(done) }()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		server.Stop()
		<-done
	}
	return nil
}

func previewConnection(addr string) (*grpc.ClientConn, context.Context, context.CancelFunc, error) {
	service := os.Getenv("RPC_TLS_SERVICE")
	if service == "" {
		service = "preview"
	}
	rpcTLS, err := rpctls.Load(service)
	if err != nil {
		return nil, nil, nil, err
	}
	conn, err := rpcTLS.Dial(addr, "preview")
	if err != nil {
		return nil, nil, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	return conn, ctx, cancel, nil
}

func checkHealth(addr string) error {
	conn, ctx, cancel, err := previewConnection(addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer cancel()
	_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	return err
}

func checkDecode(addr, path string) error {
	data := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0xf4, 0x22, 0x7f,
		0x8a, 0x00, 0x00, 0x00, 0x0e, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0xf0,
		0x1f, 0x04, 0x01, 0x10, 0xf8, 0x03, 0xfd, 0x4e, 0x95, 0xc1, 0x6f, 0x00, 0x00, 0x00, 0x00, 0x49,
		0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
	}
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		data, err = io.ReadAll(io.LimitReader(file, preview.MaxInput+1))
		if err != nil {
			return err
		}
		if len(data) > preview.MaxInput {
			return errors.New("check image exceeds 10 MiB")
		}
	}
	conn, ctx, cancel, err := previewConnection(addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer cancel()
	result, err := preview.NewRPCClient(conn).Decode(ctx, data)
	if err != nil {
		return err
	}
	if path == "" && (result.Width != 2 || result.Height != 1 || len(result.RGB) != 6) {
		return errors.New("unexpected preview self-check response")
	}
	return nil
}
