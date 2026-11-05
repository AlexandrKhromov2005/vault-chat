// Package ratelimit provides bounded, process-local request admission.
package ratelimit

import (
	"context"
	"math"
	"sync"
	"time"
)

// Result describes the quota remaining after an admission attempt.
type Result struct {
	Allowed    bool
	Limit      int
	Remaining  int
	RetryAfter time.Duration
}

type bucket struct {
	tokens  float64
	updated time.Time
}

// MemoryLimiter keeps at most maxKeys buckets. Only fully refilled buckets
// may be removed: key flooding cannot reset an existing client's quota.
type MemoryLimiter struct {
	mu          sync.Mutex
	buckets     map[string]bucket
	interval    time.Duration
	burst       int
	maxKeys     int
	nextCleanup time.Time
	now         func() time.Time
}

// NewMemoryLimiter grants burst attempts and refills one per interval.
// Nonpositive settings are clamped to their smallest positive values.
func NewMemoryLimiter(interval time.Duration, burst, maxKeys int) *MemoryLimiter {
	return &MemoryLimiter{
		buckets: make(map[string]bucket), interval: max(interval, time.Nanosecond),
		burst: max(burst, 1), maxKeys: max(maxKeys, 1), now: time.Now,
	}
}

// Allow rejects new keys when the table is full, rather than evicting quotas.
func (l *MemoryLimiter) Allow(ctx context.Context, key string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, exists := l.buckets[key]
	if !exists {
		if len(l.buckets) >= l.maxKeys && !now.Before(l.nextCleanup) {
			for k, v := range l.buckets {
				if l.refill(v, now).tokens >= float64(l.burst) {
					delete(l.buckets, k)
				}
			}
			l.nextCleanup = now.Add(l.interval)
		}
		if len(l.buckets) >= l.maxKeys {
			return Result{Limit: l.burst, RetryAfter: l.interval}, nil
		}
		b = bucket{tokens: float64(l.burst), updated: now}
	}
	b = l.refill(b, now)
	result := Result{Limit: l.burst}
	if b.tokens >= 1 {
		b.tokens--
		result.Allowed = true
	} else {
		result.RetryAfter = time.Duration(math.Ceil((1 - b.tokens) * float64(l.interval)))
	}
	result.Remaining = int(b.tokens)
	l.buckets[key] = b
	return result, nil
}

// Refund returns one token taken from the bucket of key by Allow, up to the
// burst. A bucket that is gone was full already, so nothing is created.
func (l *MemoryLimiter) Refund(_ context.Context, key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if b, exists := l.buckets[key]; exists {
		b = l.refill(b, l.now())
		b.tokens = min(float64(l.burst), b.tokens+1)
		l.buckets[key] = b
	}
	return nil
}

func (l *MemoryLimiter) refill(b bucket, now time.Time) bucket {
	if now.After(b.updated) {
		b.tokens = min(float64(l.burst), b.tokens+float64(now.Sub(b.updated))/float64(l.interval))
		b.updated = now
	}
	return b
}
