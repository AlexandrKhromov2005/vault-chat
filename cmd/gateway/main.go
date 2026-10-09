// Command gateway runs the Vault Chat API gateway: the public REST entry
// point that authenticates requests and forwards them to backend services.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/ratelimit"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/router"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/config"
	sharedlogger "github.com/AlexandrKhromov2005/vault-chat/internal/shared/logger"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/requestid"
)

// shutdownTimeout bounds how long in-flight requests may take to finish
// after SIGINT/SIGTERM.
const shutdownTimeout = 15 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("gateway failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadGateway()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	logger := sharedlogger.New(cfg.LogLevel)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Service-to-service traffic is plaintext until mTLS lands
	// (ARCHITECTURE.md section 3.3); the auth service does not serve TLS yet.
	authConn, err := grpc.NewClient(cfg.AuthGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(requestid.UnaryClientInterceptor()),
	)
	if err != nil {
		return fmt.Errorf("failed to create auth client: %w", err)
	}
	defer func() { _ = authConn.Close() }()
	authClient := authv1.NewAuthServiceClient(authConn)
	authHealth := healthpb.NewHealthClient(authConn)

	redisClient := ratelimit.NewRedisClient(cfg.RedisAddr, cfg.RedisPassword)
	defer func() { _ = redisClient.Close() }()
	limiter, err := ratelimit.NewRedisLimiter(redisClient, cfg.RateLimitPerMinute, cfg.RateLimitBurst)
	if err != nil {
		return fmt.Errorf("failed to init rate limiter: %w", err)
	}

	// Redis is deliberately not a readiness dependency: the rate limiter
	// fails open, so the gateway keeps serving without it.
	health := handler.NewHealth(map[string]handler.Check{
		"auth": func(ctx context.Context) error {
			resp, err := authHealth.Check(ctx, &healthpb.HealthCheckRequest{})
			if err != nil {
				return err
			}
			if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
				return fmt.Errorf("auth service is %s", resp.GetStatus())
			}
			return nil
		},
	}, logger)

	server := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: router.New(router.Deps{
			Auth:               handler.NewAuthHandler(authClient, logger),
			Health:             health,
			TokenValidator:     authClient,
			Limiter:            limiter,
			Logger:             logger,
			CORSAllowedOrigins: cfg.CORSAllowedOrigins,
			MaxBodyBytes:       cfg.MaxBodyBytes,
			RequestTimeout:     cfg.RequestTimeout,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		// Backstop only: API request bodies are cut off at the request
		// deadline by middleware.Timeout.
		ReadTimeout:  cfg.RequestTimeout + 5*time.Second,
		WriteTimeout: cfg.RequestTimeout + 5*time.Second,
		IdleTimeout:  60 * time.Second,
		ErrorLog:     slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	tlsEnabled := cfg.TLSCertPath != ""
	serveErr := make(chan error, 1)
	go func() {
		if tlsEnabled {
			server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13}
			serveErr <- server.ListenAndServeTLS(cfg.TLSCertPath, cfg.TLSKeyPath)
			return
		}
		serveErr <- server.ListenAndServe()
	}()

	if !tlsEnabled {
		logger.Warn("TLS is disabled; set GATEWAY_TLS_CERT_PATH and GATEWAY_TLS_KEY_PATH outside development")
	}
	logger.Info("gateway listening", "addr", cfg.HTTPAddr, "tls", tlsEnabled, "auth_addr", cfg.AuthGRPCAddr)

	select {
	case err := <-serveErr:
		return fmt.Errorf("HTTP server failed: %w", err)
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("failed to shut down gracefully: %w", err)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("HTTP server failed: %w", err)
	}
	return nil
}
