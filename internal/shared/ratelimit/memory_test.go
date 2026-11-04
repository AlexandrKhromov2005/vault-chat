package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMemoryLimiterRefillAndCapacity(t *testing.T) {
	l := NewMemoryLimiter(time.Minute, 2, 2)
	now := time.Now()
	l.now = func() time.Time { return now }
	ctx := context.Background()
	check := func(key string, allowed bool) Result {
		t.Helper()
		got, err := l.Allow(ctx, key)
		require.NoError(t, err)
		require.Equal(t, allowed, got.Allowed)
		return got
	}
	check("victim", true)
	check("victim", true)
	check("other", true)
	// Filling the table must never evict a depleted bucket.
	check("flood", false)
	check("victim", false)
	now = now.Add(30 * time.Second)
	require.Equal(t, 30*time.Second, check("victim", false).RetryAfter)
	now = now.Add(30 * time.Second)
	check("victim", true)
	check("victim", false)
	now = now.Add(2 * time.Minute)
	check("flood", true)
	check("victim", true)
	check("victim", true)
	check("victim", false)
	// Rewinding the clock cannot refill a bucket twice.
	now = now.Add(-time.Minute)
	check("victim", false)
}

func TestMemoryLimiterConcurrentAttempts(t *testing.T) {
	l := NewMemoryLimiter(time.Minute, 5, 10)
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			got, err := l.Allow(context.Background(), "account")
			if err != nil {
				t.Error(err)
			}
			if got.Allowed {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	require.Equal(t, int32(5), allowed.Load())
}

func TestMemoryLimiterRejectsCanceledRequestsAndBoundsKeys(t *testing.T) {
	l := NewMemoryLimiter(time.Minute, 5, 10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := l.Allow(ctx, "account")
	require.ErrorIs(t, err, context.Canceled)
	for i := range 100 {
		_, err := l.Allow(context.Background(), fmt.Sprint(i))
		require.NoError(t, err)
	}
	require.Len(t, l.buckets, 10)
}

func TestMemoryLimiterRefund(t *testing.T) {
	l := NewMemoryLimiter(time.Minute, 2, 2)
	now := time.Now()
	l.now = func() time.Time { return now }
	ctx := context.Background()
	allowed := func(key string) bool {
		t.Helper()
		got, err := l.Allow(ctx, key)
		require.NoError(t, err)
		return got.Allowed
	}
	require.True(t, allowed("a"))
	require.True(t, allowed("a"))
	require.False(t, allowed("a"))
	require.NoError(t, l.Refund(ctx, "a"))
	require.True(t, allowed("a"))
	require.False(t, allowed("a"))

	// Refunds never raise a bucket above its burst.
	for range 5 {
		require.NoError(t, l.Refund(ctx, "a"))
	}
	require.True(t, allowed("a"))
	require.True(t, allowed("a"))
	require.False(t, allowed("a"))

	// A refund for a key without a bucket takes no table slot.
	require.NoError(t, l.Refund(ctx, "missing"))
	require.True(t, allowed("b"))
	require.False(t, allowed("c"), "the table holds two buckets")
}
