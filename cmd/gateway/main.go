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
	chatv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/chat/v1"
	mediav1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/media/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/ratelimit"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/realtime"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/router"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/config"
	sharedlogger "github.com/AlexandrKhromov2005/vault-chat/internal/shared/logger"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/requestid"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/telemetry"
	"github.com/AlexandrKhromov2005/vault-chat/web"
)

// shutdownTimeout bounds how long in-flight requests may take to finish
// after SIGINT/SIGTERM.
const shutdownTimeout = 15 * time.Second

// reauthInterval is the shortest time between in-band token refreshes of a
// WebSocket connection; clients refresh minutes before their token expires.
const reauthInterval = 10 * time.Second

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
	metrics := telemetry.New("gateway")
	stopMetrics, err := metrics.Listen(ctx, os.Getenv("GATEWAY_METRICS_ADDR"))
	if err != nil {
		return fmt.Errorf("listen metrics: %w", err)
	}
	defer stopMetrics()

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

	chatConn, err := grpc.NewClient(cfg.ChatGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithUnaryInterceptor(requestid.UnaryClientInterceptor()))
	if err != nil {
		return fmt.Errorf("failed to create chat client: %w", err)
	}
	defer func() { _ = chatConn.Close() }()
	chatClient := chatv1.NewChatServiceClient(chatConn)
	chatHealth := healthpb.NewHealthClient(chatConn)

	redisClient := ratelimit.NewRedisClient(cfg.RedisAddr, cfg.RedisPassword)
	defer func() { _ = redisClient.Close() }()
	limiter, err := ratelimit.NewRedisLimiter(redisClient, cfg.RateLimitPerMinute, cfg.RateLimitBurst)
	if err != nil {
		return fmt.Errorf("failed to init rate limiter: %w", err)
	}

	// Every instance subscribes to the event channels of its own WebSocket
	// users on the same Redis; nothing is shared between instances.
	stream := realtime.NewRedisStream(redisClient)
	hub := realtime.NewHub(stream, int(cfg.WebSocket.MaxConnectionsPerUser), int(cfg.WebSocket.SendBuffer))
	go func() {
		if err := stream.Run(ctx, hub); err != nil {
			logger.Error("event stream stopped", "error", err)
		}
	}()

	// Redis is deliberately not a readiness dependency: the rate limiter
	// fails open, so the gateway keeps serving without it.
	checks := map[string]handler.Check{
		"auth": checkGRPCHealth(authHealth, "auth"),
		"chat": checkGRPCHealth(chatHealth, "chat"),
	}
	var mediaHandler *handler.MediaHandler
	if cfg.MediaGRPCAddr != "" {
		conn, err := grpc.NewClient(cfg.MediaGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithUnaryInterceptor(requestid.UnaryClientInterceptor()))
		if err != nil {
			return fmt.Errorf("create media client: %w", err)
		}
		defer conn.Close()
		mediaHandler = handler.NewMediaHandler(mediav1.NewMediaServiceClient(conn), logger)
		checks["media"] = checkGRPCHealth(healthpb.NewHealthClient(conn), "media")
	}
	health := handler.NewHealth(checks, logger)

	websockets := handler.NewRealtime(hub, authClient, handler.RealtimeOptions{
		AllowedOrigins: cfg.CORSAllowedOrigins,
		AuthTimeout:    cfg.WebSocket.AuthTimeout,
		ReauthInterval: reauthInterval,
		PingInterval:   cfg.WebSocket.PingInterval,
		WriteTimeout:   cfg.WebSocket.WriteTimeout,
		// The rate limiter fails open without Redis; these limits do not.
		MaxConnections:     int(cfg.WebSocket.MaxConnections),
		MaxUnauthenticated: int(cfg.WebSocket.MaxUnauthenticatedConnections),
	}, logger)

	metrics.Gauge("vault_websocket_connections", "Admitted WebSocket connections including pending authentication.", func() float64 { n, _ := websockets.ConnectionCounts(); return float64(n) })
	metrics.Gauge("vault_websocket_unauthenticated_connections", "Admitted WebSocket connections awaiting authentication.", func() float64 { _, n := websockets.ConnectionCounts(); return float64(n) })
	server := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: metrics.HTTP(router.New(router.Deps{
			Auth:               handler.NewAuthHandler(authClient, logger),
			Chat:               handler.NewChatHandler(chatClient, logger),
			Health:             health,
			Media:              mediaHandler,
			TokenValidator:     authClient,
			Limiter:            limiter,
			Logger:             logger,
			CORSAllowedOrigins: cfg.CORSAllowedOrigins,
			MaxBodyBytes:       cfg.MaxBodyBytes,
			RequestTimeout:     cfg.RequestTimeout,
			Realtime:           websockets,
			Web:                handler.NewWebApp(web.Files()),
		})),
		ReadHeaderTimeout: 5 * time.Second,
		// Backstop only: API request bodies are cut off at the request
		// deadline by middleware.Timeout. Hijacking clears both deadlines,
		// so they do not limit WebSocket connections.
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
	// server.Shutdown does not see hijacked WebSocket connections. The
	// handler closes all of them with 1001, authenticated or not, so that
	// clients reconnect to another instance, and waits until the close
	// handshakes are done; the hub stops accepting subscriptions meanwhile.
	websocketsDone := make(chan error, 1)
	go func() { websocketsDone <- websockets.Shutdown(shutdownCtx) }()
	hubDone := make(chan error, 1)
	go func() { hubDone <- hub.Shutdown(shutdownCtx) }()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("failed to shut down gracefully: %w", err)
	}
	if err := <-websocketsDone; err != nil {
		return fmt.Errorf("failed to close WebSocket connections: %w", err)
	}
	if err := <-hubDone; err != nil {
		return fmt.Errorf("failed to detach WebSocket connections: %w", err)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("HTTP server failed: %w", err)
	}
	return nil
}

func checkGRPCHealth(client healthpb.HealthClient, name string) handler.Check {
	return func(ctx context.Context) error {
		resp, err := client.Check(ctx, &healthpb.HealthCheckRequest{})
		if err != nil {
			return err
		}
		if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
			return fmt.Errorf("%s service is %s", name, resp.GetStatus())
		}
		return nil
	}
}
