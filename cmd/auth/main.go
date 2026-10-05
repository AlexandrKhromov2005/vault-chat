// Command auth runs the Vault Chat auth service.
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

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/config"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/jwt"
	"github.com/AlexandrKhromov2005/vault-chat/migrations"
)

func main() {
	if err := run(); err != nil {
		slog.Error("auth service failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	privatePEM, err := os.ReadFile(cfg.JWTPrivateKeyPath)
	if err != nil {
		return fmt.Errorf("failed to read JWT private key: %w", err)
	}
	publicPEM, err := os.ReadFile(cfg.JWTPublicKeyPath)
	if err != nil {
		return fmt.Errorf("failed to read JWT public key: %w", err)
	}
	tokenManager, err := jwt.NewManager(privatePEM, publicPEM, cfg.JWTAccessTTL, cfg.JWTRefreshTTL)
	if err != nil {
		return fmt.Errorf("failed to init JWT manager: %w", err)
	}

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("failed to create connection pool: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	if err := repository.Migrate(ctx, pool, migrations.AuthFS, "auth"); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	svc, err := service.NewService(
		repository.NewUserRepository(pool),
		repository.NewSessionRepository(pool),
		service.NewArgon2idHasher(service.Argon2idParams{
			MemoryKiB:   cfg.Argon2MemoryKiB,
			Time:        cfg.Argon2Time,
			Parallelism: cfg.Argon2Parallelism,
			SaltLength:  16,
			KeyLength:   32,
		}),
		tokenManager,
		logger,
	)
	if err != nil {
		return fmt.Errorf("failed to init auth service: %w", err)
	}

	listener, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", cfg.GRPCAddr, err)
	}

	grpcServer := grpc.NewServer()
	authv1.RegisterAuthServiceServer(grpcServer, handler.NewAuthGRPCHandler(svc))
	reflection.Register(grpcServer)

	go func() {
		<-ctx.Done()
		logger.Info("shutting down")
		grpcServer.GracefulStop()
	}()

	logger.Info("auth service listening", "addr", cfg.GRPCAddr)
	if err := grpcServer.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("gRPC server failed: %w", err)
	}
	return nil
}

func newLogger(level string) *slog.Logger {
	levels := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}
	parsed, ok := levels[level]
	if !ok {
		parsed = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parsed}))
}
