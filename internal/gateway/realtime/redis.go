package realtime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/redis/go-redis/v9"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/eventbus"
)

// Sink receives what a stream relays. The hub implements it.
type Sink interface {
	Deliver(userID string, payload []byte)
	Interrupt(userID string)
}

// RedisStream multiplexes the event channels of the users connected to this
// instance over one Redis Pub/Sub connection.
type RedisStream struct {
	pubsub   *redis.PubSub
	messages <-chan any
	mu       sync.Mutex
	// pending holds a waiter per subscription that Redis has yet to confirm.
	pending map[string][]chan struct{}
}

// NewRedisStream opens the stream lazily; Run must be running for Subscribe to
// return. Options tune go-redis, e.g. its health check interval.
func NewRedisStream(client *redis.Client, options ...redis.ChannelOption) *RedisStream {
	pubsub := client.Subscribe(context.Background())
	return &RedisStream{pubsub: pubsub, messages: pubsub.ChannelWithSubscriptions(options...), pending: make(map[string][]chan struct{})}
}

// Subscribe returns once Redis has confirmed the subscription to the user's
// channel. On failure the channel is unsubscribed, so a reconnection does not
// restore a subscription the caller believes is absent.
func (s *RedisStream) Subscribe(ctx context.Context, userID string) error {
	channel := eventbus.UserChannel(userID)
	confirmed := make(chan struct{})
	s.mu.Lock()
	s.pending[channel] = append(s.pending[channel], confirmed)
	s.mu.Unlock()
	err := s.pubsub.Subscribe(ctx, channel)
	if err == nil {
		select {
		case <-confirmed:
			return nil
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	s.mu.Lock()
	s.pending[channel] = slices.DeleteFunc(s.pending[channel], func(waiter chan struct{}) bool { return waiter == confirmed })
	if len(s.pending[channel]) == 0 {
		delete(s.pending, channel)
	}
	s.mu.Unlock()
	_ = s.pubsub.Unsubscribe(context.WithoutCancel(ctx), channel)
	return fmt.Errorf("subscribe to %s: %w", channel, err)
}

// Unsubscribe stops relaying the user's events.
func (s *RedisStream) Unsubscribe(ctx context.Context, userID string) error {
	if err := s.pubsub.Unsubscribe(ctx, eventbus.UserChannel(userID)); err != nil {
		return fmt.Errorf("unsubscribe from user events: %w", err)
	}
	return nil
}

// Run relays events to sink until ctx ends, then closes the stream. A
// confirmation that nobody awaits is go-redis restoring a subscription after
// a lost connection: events published meanwhile are gone, so the user is
// interrupted.
func (s *RedisStream) Run(ctx context.Context, sink Sink) error {
	defer func() { _ = s.pubsub.Close() }()
	for {
		select {
		case <-ctx.Done():
			return nil
		case received, ok := <-s.messages:
			if !ok {
				return errors.New("realtime: redis stream closed")
			}
			switch message := received.(type) {
			case *redis.Subscription:
				if message.Kind == "subscribe" && !s.confirm(message.Channel) {
					if user, ok := eventbus.UserFromChannel(message.Channel); ok {
						sink.Interrupt(user)
					}
				}
			case *redis.Message:
				if user, ok := eventbus.UserFromChannel(message.Channel); ok {
					sink.Deliver(user, []byte(message.Payload))
				}
			}
		}
	}
}

// confirm wakes the oldest waiter for channel and reports whether there was one.
func (s *RedisStream) confirm(channel string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	waiters := s.pending[channel]
	if len(waiters) == 0 {
		return false
	}
	close(waiters[0])
	if len(waiters) == 1 {
		delete(s.pending, channel)
	} else {
		s.pending[channel] = waiters[1:]
	}
	return true
}
