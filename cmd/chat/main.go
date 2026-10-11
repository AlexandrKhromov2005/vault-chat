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
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/config"
	sharedlogger "github.com/AlexandrKhromov2005/vault-chat/internal/shared/logger"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/migration"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/requestid"
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
	server := grpc.NewServer(grpc.MaxRecvMsgSize(1<<20), grpc.ChainUnaryInterceptor(requestid.UnaryServerInterceptor(), handler.UnaryTimeout(cfg.RequestTimeout)))
	chatv1.RegisterChatServiceServer(server, handler.NewGRPCHandler(service.NewService(repository.NewRoomRepository(pool)), authv1.NewAuthServiceClient(authConn), logger))
	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(server, healthServer)
	reflection.Register(server)
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	logger.Info("chat service listening", "addr", cfg.GRPCAddr)
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
