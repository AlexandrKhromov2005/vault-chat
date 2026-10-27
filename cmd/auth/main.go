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
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/events"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/config"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/jwt"
	sharedlogger "github.com/AlexandrKhromov2005/vault-chat/internal/shared/logger"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/requestid"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/rpctls"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/telemetry"
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

	logger := sharedlogger.New(cfg.LogLevel)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	metrics := telemetry.New("auth")
	stopMetrics, err := metrics.Listen(ctx, os.Getenv("AUTH_METRICS_ADDR"))
	if err != nil {
		return fmt.Errorf("listen metrics: %w", err)
	}
	defer stopMetrics()
	rpcTLS, err := rpctls.Load("auth")
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

	redisClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword, ContextTimeoutEnabled: true, MaxRetries: -1, DialTimeout: 250 * time.Millisecond, ReadTimeout: 250 * time.Millisecond, WriteTimeout: 250 * time.Millisecond})
	defer redisClient.Close()
	emailOpts, mailWorker, err := emailOptions(cfg, pool, logger)
	if err != nil {
		return fmt.Errorf("configure account email: %w", err)
	}
	options := append([]service.Option{
		service.WithInvitations(repository.NewUserRepository(pool)),
		service.WithSessionNotifier(events.Notifier{Client: redisClient}),
	}, emailOpts...)
	svc, err := service.NewService(
		repository.NewUserRepository(pool),
		repository.NewSessionRepository(pool, cfg.EmailLegacyDeadline),
		service.NewArgon2idHasher(service.Argon2idParams{
			MemoryKiB:   cfg.Argon2MemoryKiB,
			Time:        cfg.Argon2Time,
			Parallelism: cfg.Argon2Parallelism,
			SaltLength:  16,
			KeyLength:   32,
		}),
		tokenManager,
		logger,
		options...,
	)
	if err != nil {
		return fmt.Errorf("failed to init auth service: %w", err)
	}
	stopMail := startMailWorker(ctx, mailWorker)
	defer stopMail()

	listener, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", cfg.GRPCAddr, err)
	}

	// The interceptor puts the caller's request id into the context, and the
	// shared logger adds it to every record logged with that context.
	grpcServer := grpc.NewServer(rpcTLS.ServerOption(), grpc.ChainStreamInterceptor(rpcTLS.Stream()), grpc.ChainUnaryInterceptor(metrics.Unary(), rpcTLS.Unary(), requestid.UnaryServerInterceptor()))
	authv1.RegisterAuthServiceServer(grpcServer, handler.NewAuthGRPCHandler(svc))
	// Standard gRPC health service: the gateway's readiness probe uses it.
	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthServer)
	if !rpcTLS.Enabled() {
		reflection.Register(grpcServer)
	}

	go func() {
		<-ctx.Done()
		logger.Info("shutting down")
		healthServer.Shutdown()
		grpcServer.GracefulStop()
	}()

	logger.Info("auth service listening", "addr", cfg.GRPCAddr)
	if err := grpcServer.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("gRPC server failed: %w", err)
	}
	return nil
}
