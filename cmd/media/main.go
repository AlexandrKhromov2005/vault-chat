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

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	mediav1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/media/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/media/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/media/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/media/storage"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/config"
	sharedlogger "github.com/AlexandrKhromov2005/vault-chat/internal/shared/logger"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/migration"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/requestid"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/telemetry"
	"github.com/AlexandrKhromov2005/vault-chat/migrations"
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
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(startup, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := migration.Apply(startup, pool, migrations.MediaFS, "media"); err != nil {
		return err
	}
	blobs, err := storage.NewS3(startup, cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket, cfg.S3Secure)
	if err != nil {
		return err
	}
	dial := func(addr string) (*grpc.ClientConn, error) {
		return grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithUnaryInterceptor(requestid.UnaryClientInterceptor()))
	}
	auth, err := dial(cfg.AuthGRPCAddr)
	if err != nil {
		return err
	}
	defer auth.Close()
	chat, err := dial(cfg.ChatGRPCAddr)
	if err != nil {
		return err
	}
	defer chat.Close()
	svc := service.New(&storage.Postgres{Pool: pool}, blobs, handler.Gate{Client: chatv1.NewChatServiceClient(chat)})
	listener, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := grpc.NewServer(grpc.MaxRecvMsgSize(128<<10), grpc.ChainUnaryInterceptor(metrics.Unary(), requestid.UnaryServerInterceptor()))
	mediav1.RegisterMediaServiceServer(server, handler.New(svc, authv1.NewAuthServiceClient(auth)))
	hs := health.NewServer()
	healthpb.RegisterHealthServer(server, hs)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweepCtx, done := context.WithTimeout(ctx, 30*time.Second)
				err := svc.Sweep(sweepCtx)
				done()
				if err != nil {
					logger.Error("media cleanup failed", "error", err)
				}
			}
		}
	}()
	defer func() { stop(); <-workerDone }()
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	logger.Info("media listening", "addr", cfg.GRPCAddr)
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
