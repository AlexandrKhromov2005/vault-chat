package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/push"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/config"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/pushbus"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/rpctls"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/telemetry"
)

// startPush runs the push dispatcher and the consumer of the signal stream
// when FCM credentials are configured (docs/push.md). The broker is not a
// startup dependency: the connection and the declaration of the stream
// keep retrying. The returned function stops both, gives the signals in
// work back to the stream and waits for sends in progress, at most 15
// seconds.
func startPush(ctx context.Context, cfg *config.Config, sessions *repository.SessionRepository, rpcTLS *rpctls.Config,
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
	conn, err := pushbus.Connect(cfg.PushBrokerURL, "auth", rpcTLS, logger)
	if err != nil {
		return nil, err
	}
	js, err := jetstream.New(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("push signal broker: %w", err)
	}
	dispatcher := push.NewDispatcher(provider, sessions, sessions, logger, push.Config{})
	consumer := push.NewConsumer(push.NewDeclaringSource(js), dispatcher, sessions, logger, push.ConsumerConfig{})
	registerPushMetrics(metrics, dispatcher, consumer)
	stream := watchStream(ctx, js, logger)
	metrics.Gauge("vault_push_stream_pending", "Push signals stored and not yet delivered to Auth.", func() float64 { return float64(stream.pending.Load()) })
	metrics.Gauge("vault_push_stream_ack_pending", "Push signals delivered to Auth and not yet acknowledged.", func() float64 { return float64(stream.ackPending.Load()) })

	runCtx, cancel := context.WithCancel(ctx)
	var running sync.WaitGroup
	running.Go(func() { dispatcher.Run(runCtx) })
	running.Go(func() { consumer.Run(runCtx) })
	running.Go(func() { push.CleanMarks(runCtx, sessions, time.Minute, logger) })
	logger.Info("push notifications enabled", "provider", "fcm")
	return func() {
		cancel()
		done := make(chan struct{})
		go func() { running.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
		}
		// Flushes the acknowledgements and the signals given back.
		if err := conn.Drain(); err != nil {
			conn.Close()
		}
	}, nil
}

func registerPushMetrics(metrics *telemetry.Metrics, dispatcher *push.Dispatcher, consumer *push.Consumer) {
	stats := func(f func(push.Stats) float64) func() float64 {
		return func() float64 { return f(dispatcher.Stats()) }
	}
	metrics.Gauge("vault_push_pending", "User and room pairs waiting in the push collapsing window.", stats(func(s push.Stats) float64 { return float64(s.Pending) }))
	metrics.Counter("vault_push_sent_total", "Push messages accepted by the provider.", stats(func(s push.Stats) float64 { return float64(s.Sent) }))
	metrics.Counter("vault_push_failures_total", "Push sends, token loads or marks that failed.", stats(func(s push.Stats) float64 { return float64(s.Failed) }))
	metrics.Counter("vault_push_dropped_total", "Recipients refused because too many pairs were pending; their signal is retried.", stats(func(s push.Stats) float64 { return float64(s.Dropped) }))
	metrics.Counter("vault_push_invalid_tokens_total", "Device tokens deleted after the provider rejected them.", stats(func(s push.Stats) float64 { return float64(s.Invalid) }))
	metrics.Counter("vault_push_malformed_signals_total", "Push signals terminated as malformed.", stats(func(s push.Stats) float64 { return float64(s.Malformed) }))
	metrics.Counter("vault_push_skipped_total", "Push sends not started because the device left an active login meanwhile.", stats(func(s push.Stats) float64 { return float64(s.Skipped) }))
	metrics.Counter("vault_push_expired_total", "Pushes not sent because their signals were older than an hour.", stats(func(s push.Stats) float64 { return float64(s.Expired) }))
	consumed := func(f func(push.ConsumerStats) float64) func() float64 {
		return func() float64 { return f(consumer.Stats()) }
	}
	metrics.Gauge("vault_push_signals_in_work", "Push signals Auth holds unacknowledged.", consumed(func(s push.ConsumerStats) float64 { return float64(s.InWork) }))
	metrics.Counter("vault_push_signal_deliveries_total", "Deliveries of push signals from the stream, redeliveries included.", consumed(func(s push.ConsumerStats) float64 { return float64(s.Received) }))
	metrics.Counter("vault_push_signals_acked_total", "Push signals acknowledged: every recipient handled.", consumed(func(s push.ConsumerStats) float64 { return float64(s.Acked) }))
	metrics.Counter("vault_push_signals_retried_total", "Push signals returned for a delayed redelivery after a temporary failure.", consumed(func(s push.ConsumerStats) float64 { return float64(s.Retried) }))
	metrics.Counter("vault_push_signals_terminated_total", "Push signals terminated: malformed or out of deliveries.", consumed(func(s push.ConsumerStats) float64 { return float64(s.Terminated) }))
	metrics.Counter("vault_push_signals_exhausted_total", "Push signals terminated after their last delivery failed.", consumed(func(s push.ConsumerStats) float64 { return float64(s.Exhausted) }))
}

// streamState is the consumer's view of the stream, refreshed in the
// background so that a scrape never waits for the broker. -1 means unknown.
type streamState struct {
	pending, ackPending atomic.Int64
}

func watchStream(ctx context.Context, js jetstream.JetStream, logger *slog.Logger) *streamState {
	state := &streamState{}
	state.pending.Store(-1)
	state.ackPending.Store(-1)
	go func() {
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			infoCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			info, err := consumerInfo(infoCtx, js)
			cancel()
			if err != nil {
				state.pending.Store(-1)
				state.ackPending.Store(-1)
				if ctx.Err() == nil {
					logger.Debug("push signal stream state unavailable", "error", err)
				}
			} else {
				state.pending.Store(int64(info.NumPending))
				state.ackPending.Store(int64(info.NumAckPending))
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return state
}

func consumerInfo(ctx context.Context, js jetstream.JetStream) (*jetstream.ConsumerInfo, error) {
	// Looking the consumer up fetches its current state.
	consumer, err := js.Consumer(ctx, pushbus.StreamName, pushbus.ConsumerName)
	if err != nil {
		return nil, err
	}
	return consumer.CachedInfo(), nil
}
