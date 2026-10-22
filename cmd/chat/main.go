// Command chat runs the authenticated Vault Chat room service.
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
	"google.golang.org/grpc/reflection"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	mediav1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/media/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/events"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/handler"
	chatmedia "github.com/AlexandrKhromov2005/vault-chat/internal/chat/media"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/config"
	sharedlogger "github.com/AlexandrKhromov2005/vault-chat/internal/shared/logger"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/migration"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/requestid"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/telemetry"
	"github.com/AlexandrKhromov2005/vault-chat/migrations"
)

func main() {
	if err := run(); err != nil {
		slog.Error("chat service failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadChat()
	if err != nil {
		return fmt.Errorf("load chat config: %w", err)
	}
	logger := sharedlogger.New(cfg.LogLevel)
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	metrics := telemetry.New("chat")
	stopMetrics, err := metrics.Listen(ctx, os.Getenv("CHAT_METRICS_ADDR"))
	if err != nil {
		return fmt.Errorf("listen metrics: %w", err)
	}
	defer stopMetrics()
	startup, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
	defer cancel()
	pool, err := pgxpool.New(startup, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("create chat database pool: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(startup); err != nil {
		return fmt.Errorf("connect chat database: %w", err)
	}
	if err := migration.Apply(startup, pool, migrations.ChatFS, "chat"); err != nil {
		return fmt.Errorf("migrate chat database: %w", err)
	}
	// Internal RPC is plaintext in development, as in Auth. mTLS is future work.
	authConn, err := grpc.NewClient(cfg.AuthGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithUnaryInterceptor(requestid.UnaryClientInterceptor()))
	if err != nil {
		return fmt.Errorf("create chat auth client: %w", err)
	}
	defer func() { _ = authConn.Close() }()
	listener, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("listen chat: %w", err)
	}
	defer func() { _ = listener.Close() }()
	// Redis is not a startup or readiness dependency: publishing is best effort
	// and clients recover missed events through catch-up.
	redisClient := events.NewRedisClient(cfg.RedisAddr, cfg.RedisPassword)
	defer func() { _ = redisClient.Close() }()
	publisher := events.NewPublisher(redisClient, logger,
		events.WithQueueSize(cfg.EventQueueSize), events.WithPublishTimeout(cfg.EventPublishTimeout))
	metrics.Gauge("vault_event_queue_pending", "Committed changes waiting for publication.", func() float64 { return float64(publisher.Stats().Pending) })
	metrics.Gauge("vault_event_queue_capacity", "Maximum queued committed changes.", func() float64 { return float64(publisher.Stats().Capacity) })
	metrics.Counter("vault_events_dropped_total", "Events discarded by queue overflow or shutdown.", func() float64 { return float64(publisher.Stats().Dropped) })
	metrics.Counter("vault_events_publish_failures_total", "Events whose Redis pipeline failed; delivery may be partial.", func() float64 { return float64(publisher.Stats().Failed) })
	metrics.Counter("vault_events_published_total", "Events successfully published to their recipients.", func() float64 { return float64(publisher.Stats().Published) })
	metrics.Counter("vault_events_encoding_failures_total", "Invalid events that could not be encoded.", func() float64 { return float64(publisher.Stats().EncodingFailures) })
	// Runs after the gRPC server stopped, so no request enqueues events any more.
	defer closePublisher(publisher, cfg.EventDrainTimeout, logger)
	rooms := repository.NewRoomRepository(pool, repository.WithNotifier(publisher))
	server := grpc.NewServer(grpc.MaxRecvMsgSize(1<<20), grpc.ChainUnaryInterceptor(metrics.Unary(), requestid.UnaryServerInterceptor(), handler.UnaryTimeout(cfg.RequestTimeout)))
	var messageOptions []service.MessageOption
	if cfg.MediaGRPCAddr != "" {
		conn, err := grpc.NewClient(cfg.MediaGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithUnaryInterceptor(requestid.UnaryClientInterceptor()))
		if err != nil {
			return fmt.Errorf("create chat media client: %w", err)
		}
		defer conn.Close()
		messageOptions = append(messageOptions, service.WithAttachmentChecker(chatmedia.Checker{Client: mediav1.NewMediaServiceClient(conn)}))
	}
	chatv1.RegisterChatServiceServer(server, handler.NewGRPCHandler(service.NewService(rooms), service.NewMessageService(rooms, messageOptions...), authv1.NewAuthServiceClient(authConn), logger))
	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(server, healthServer)
	reflection.Register(server)
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	logger.Info("chat service listening", "addr", cfg.GRPCAddr, "redis_addr", cfg.RedisAddr)
	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			return fmt.Errorf("serve chat: %w", err)
		}
		return nil
	case <-ctx.Done():
	}
	healthServer.Shutdown()
	done := make(chan struct{})
	go func() { server.GracefulStop(); close(done) }()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		server.Stop()
		<-done
	}
	logger.Info("chat service stopped")
	return nil
}

// closePublisher publishes the queued events for at most timeout; the rest
// are lost like any other dropped event and clients catch up.
func closePublisher(publisher *events.Publisher, timeout time.Duration, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := publisher.Close(ctx); err != nil {
		logger.Warn("chat events not drained", "error", err, "dropped_total", publisher.Dropped())
		return
	}
	logger.Info("chat events drained", "dropped_total", publisher.Dropped())
}
