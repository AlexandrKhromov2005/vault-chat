package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// newTestLimiter returns a limiter on top of an in-memory Redis whose clock
// is controlled by the returned advance function. The rate is 60 per minute,
// one token per second, which keeps expected durations easy to read.
func newTestLimiter(t *testing.T, burst uint32) (*RedisLimiter, *miniredis.Miniredis, func(time.Duration)) {
	t.Helper()

	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	limiter, err := NewRedisLimiter(client, 60, burst)
	require.NoError(t, err)

	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }
	return limiter, server, func(d time.Duration) { now = now.Add(d) }
}

func TestNewRedisLimiter_RejectsZeroParameters(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:0"})
	t.Cleanup(func() { _ = client.Close() })

	_, err := NewRedisLimiter(client, 0, 10)
	require.Error(t, err)
	_, err = NewRedisLimiter(client, 60, 0)
	require.Error(t, err)
}

func TestRedisLimiter_Allow(t *testing.T) {
	ctx := context.Background()

	t.Run("allows a burst then rejects", func(t *testing.T) {
		limiter, _, _ := newTestLimiter(t, 3)

		for remaining := 2; remaining >= 0; remaining-- {
			res, err := limiter.Allow(ctx, "ip:10.0.0.1")
			require.NoError(t, err)
			require.True(t, res.Allowed)
			require.Equal(t, 3, res.Limit)
			require.Equal(t, remaining, res.Remaining)
		}

		res, err := limiter.Allow(ctx, "ip:10.0.0.1")
		require.NoError(t, err)
		require.False(t, res.Allowed)
		require.Equal(t, 0, res.Remaining)
		// 60 per minute refills one token per second.
		require.Equal(t, time.Second, res.RetryAfter)
	})

	t.Run("refills over time", func(t *testing.T) {
		limiter, _, advance := newTestLimiter(t, 1)

		res, err := limiter.Allow(ctx, "ip:10.0.0.1")
		require.NoError(t, err)
		require.True(t, res.Allowed)

		advance(500 * time.Millisecond)
		res, err = limiter.Allow(ctx, "ip:10.0.0.1")
		require.NoError(t, err)
		require.False(t, res.Allowed)
		require.Equal(t, 500*time.Millisecond, res.RetryAfter)

		advance(500 * time.Millisecond)
		res, err = limiter.Allow(ctx, "ip:10.0.0.1")
		require.NoError(t, err)
		require.True(t, res.Allowed)
	})

	t.Run("refill never exceeds the burst", func(t *testing.T) {
		limiter, _, advance := newTestLimiter(t, 2)

		_, err := limiter.Allow(ctx, "ip:10.0.0.1")
		require.NoError(t, err)
		advance(time.Hour)

		res, err := limiter.Allow(ctx, "ip:10.0.0.1")
		require.NoError(t, err)
		require.Equal(t, 1, res.Remaining)
	})

	t.Run("keys are independent", func(t *testing.T) {
		limiter, _, _ := newTestLimiter(t, 1)

		res, err := limiter.Allow(ctx, "ip:10.0.0.1")
		require.NoError(t, err)
		require.True(t, res.Allowed)

		res, err = limiter.Allow(ctx, "ip:10.0.0.2")
		require.NoError(t, err)
		require.True(t, res.Allowed)
	})

	t.Run("buckets are namespaced and expire", func(t *testing.T) {
		limiter, server, _ := newTestLimiter(t, 30)

		_, err := limiter.Allow(ctx, "user:u-1")
		require.NoError(t, err)

		require.True(t, server.Exists("rate:user:u-1"))
		ttl := server.TTL("rate:user:u-1")
		require.Greater(t, ttl, time.Duration(0))
		// A full refill of 30 tokens at 1/s takes 30s; the bucket must not
		// outlive that by much.
		require.LessOrEqual(t, ttl, 31*time.Second)
	})

	t.Run("redis failure is reported", func(t *testing.T) {
		limiter, server, _ := newTestLimiter(t, 1)
		server.Close()

		_, err := limiter.Allow(ctx, "ip:10.0.0.1")
		require.Error(t, err)
	})
}
