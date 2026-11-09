// Command media runs private, room-authorized file storage.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	mediav1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/media/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/media/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/media/preview"
	"github.com/AlexandrKhromov2005/vault-chat/internal/media/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/media/storage"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/config"
	sharedlogger "github.com/AlexandrKhromov2005/vault-chat/internal/shared/logger"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/requestid"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/rpctls"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/telemetry"
)

func main() {
	if err := run(); err != nil {
		slog.Error("media failed", "error", err)
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config.LoadMedia()
	if err != nil {
		return err
	}
	logger := sharedlogger.New(cfg.LogLevel)
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	metrics := telemetry.New("media")
	stopMetrics, err := metrics.Listen(ctx, os.Getenv("MEDIA_METRICS_ADDR"))
	if err != nil {
		return fmt.Errorf("listen metrics: %w", err)
	}
	defer stopMetrics()
	rpcTLS, err := rpctls.Load("media")
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
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(startup, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	blobs, err := storage.NewS3(startup, cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket, cfg.S3Secure)
	if err != nil {
		return err
	}
	auth, err := rpcTLS.Dial(cfg.AuthGRPCAddr, "auth")
	if err != nil {
		return err
	}
	defer auth.Close()
	chat, err := rpcTLS.Dial(cfg.ChatGRPCAddr, "chat")
	if err != nil {
		return err
	}
	defer chat.Close()
	repo := &storage.Postgres{Pool: pool}
	svc := service.New(repo, blobs, handler.Gate{Client: chatv1.NewChatServiceClient(chat)})
	var previews *service.PreviewScheduler
	if len(cfg.PreviewGRPCAddrs) != 0 {
		workers := make([]preview.Decoder, 0, len(cfg.PreviewGRPCAddrs))
		for _, address := range cfg.PreviewGRPCAddrs {
			conn, err := rpcTLS.Dial(address, "preview")
			if err != nil {
				return fmt.Errorf("dial preview worker: %w", err)
			}
			defer conn.Close()
			workers = append(workers, preview.NewRPCClient(conn))
		}
		previews = service.NewPreviewScheduler(repo, blobs, workers, uuid.NewString())
		metrics.Gauge("vault_media_preview_queue_jobs", "Pending and running preview jobs.", func() float64 {
			count, _, err := previewStats(ctx, repo)
			if err != nil {
				return 0
			}
			return float64(count)
		})
		metrics.Gauge("vault_media_preview_queue_oldest_seconds", "Age of the oldest unfinished preview job.", func() float64 {
			_, age, err := previewStats(ctx, repo)
			if err != nil {
				return 0
			}
			return age
		})
	}
	listener, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := grpc.NewServer(rpcTLS.ServerOption(), grpc.ChainStreamInterceptor(rpcTLS.Stream()), grpc.MaxRecvMsgSize(128<<10), grpc.ChainUnaryInterceptor(metrics.Unary(), rpcTLS.Unary(), requestid.UnaryServerInterceptor()))
	mediav1.RegisterMediaServiceServer(server, handler.New(svc, authv1.NewAuthServiceClient(auth)))
	hs := health.NewServer()
	healthpb.RegisterHealthServer(server, hs)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		maintainTicker := time.NewTicker(5 * time.Minute)
		defer maintainTicker.Stop()
		var previewTicker *time.Ticker
		var previewTicks <-chan time.Time
		if previews != nil {
			previewTicker = time.NewTicker(500 * time.Millisecond)
			previewTicks = previewTicker.C
			defer previewTicker.Stop()
		}
		maintain := func() {
			sweepCtx, done := context.WithTimeout(ctx, 30*time.Second)
			defer done()
			if err := svc.Sweep(sweepCtx); err != nil {
				logger.Error("media cleanup failed", "error", err)
			}
			if previews != nil {
				if err := previews.Maintain(sweepCtx); err != nil {
					logger.Error("preview maintenance failed", "error", err)
				}
			}
		}
		maintain()
		for {
			select {
			case <-ctx.Done():
				return
			case <-maintainTicker.C:
				maintain()
			case <-previewTicks:
				runCtx, done := context.WithTimeout(ctx, 30*time.Second)
				err := previews.RunOnce(runCtx)
				done()
				if err != nil && !errors.Is(err, context.Canceled) {
					logger.Error("preview scheduling failed", "error", err)
				}
			}
		}
	}()
	defer func() { stop(); <-workerDone }()
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	logger.Info("media listening", "addr", cfg.GRPCAddr, "preview_workers", len(cfg.PreviewGRPCAddrs))
	select {
	case err := <-serveErr:
		if !errors.Is(err, grpc.ErrServerStopped) {
			return fmt.Errorf("serve media: %w", err)
		}
		return nil
	case <-ctx.Done():
	}
	hs.Shutdown()
	done := make(chan struct{})
	go func() { server.GracefulStop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		server.Stop()
		<-done
	}
	return nil
}

func previewStats(ctx context.Context, repo *storage.Postgres) (int, float64, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return repo.PreviewQueueStats(ctx)
}
