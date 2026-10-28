package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/push"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/config"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/telemetry"
)

// startPush runs the push dispatcher when FCM credentials are configured.
// The returned function stops it and waits for sends in progress, at most
// 15 seconds.
func startPush(ctx context.Context, cfg *config.Config, sessions *repository.SessionRepository, client *redis.Client,
	metrics *telemetry.Metrics, logger *slog.Logger) (func(), error) {
	if cfg.PushFCMCredentialsFile == "" {
		logger.Info("push notifications disabled")
		return func() {}, nil
	}
	data, err := privateFile(cfg.PushFCMCredentialsFile, push.MaxCredentialsBytes)
	if err != nil {
		return nil, errors.New("AUTH_PUSH_FCM_CREDENTIALS_FILE must be a private readable file")
	}
	provider, err := push.NewFCM(data)
	clear(data)
	if err != nil {
		return nil, fmt.Errorf("AUTH_PUSH_FCM_CREDENTIALS_FILE: %w", err)
	}
	dispatcher := push.NewDispatcher(provider, sessions, logger, push.Config{})
	stats := func(f func(push.Stats) float64) func() float64 {
		return func() float64 { return f(dispatcher.Stats()) }
	}
	metrics.Gauge("vault_push_pending", "User and room pairs waiting in the push collapsing window.", stats(func(s push.Stats) float64 { return float64(s.Pending) }))
	metrics.Counter("vault_push_sent_total", "Push messages accepted by the provider.", stats(func(s push.Stats) float64 { return float64(s.Sent) }))
	metrics.Counter("vault_push_failures_total", "Push sends or token loads that failed.", stats(func(s push.Stats) float64 { return float64(s.Failed) }))
	metrics.Counter("vault_push_dropped_total", "Push signals dropped because too many pairs were pending.", stats(func(s push.Stats) float64 { return float64(s.Dropped) }))
	metrics.Counter("vault_push_invalid_tokens_total", "Device tokens deleted after the provider rejected them.", stats(func(s push.Stats) float64 { return float64(s.Invalid) }))
	metrics.Counter("vault_push_malformed_signals_total", "Push signals dropped as malformed.", stats(func(s push.Stats) float64 { return float64(s.Malformed) }))

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{}, 2)
	go func() { dispatcher.Run(runCtx); done <- struct{}{} }()
	go func() {
		// Redis may be down at start or later; keep resubscribing.
		for runCtx.Err() == nil {
			if err := push.Subscribe(runCtx, client, dispatcher, logger); err != nil {
				logger.Warn("push signal subscription failed", "error", err)
			}
			select {
			case <-runCtx.Done():
			case <-time.After(time.Second):
			}
		}
		done <- struct{}{}
	}()
	logger.Info("push notifications enabled", "provider", "fcm")
	return func() {
		cancel()
		deadline := time.After(15 * time.Second)
		for range 2 {
			select {
			case <-done:
			case <-deadline:
				return
			}
		}
	}, nil
}
