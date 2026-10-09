// Package ratelimit implements the Redis-backed token bucket the gateway uses
// to rate limit clients (ARCHITECTURE.md sections 2.1 and 4.2). Buckets live
// in Redis so that every gateway instance enforces the same limit.
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// keyPrefix namespaces bucket keys in Redis: rate:<key>.
const keyPrefix = "rate:"

// unitsPerToken scales token counts so that the refill arithmetic stays in
// integers: refilling perMinute tokens per minute adds exactly perMinute
// units per millisecond.
const unitsPerToken = 60_000

// tokenBucket atomically refills and takes one token from the bucket stored
// in the hash KEYS[1] (fields: units, ts). It returns
// {allowed (0|1), whole tokens remaining, milliseconds until the next token}.
var tokenBucket = redis.NewScript(`
local per_minute = tonumber(ARGV[1])
local capacity = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local ttl = tonumber(ARGV[4])
local unit = tonumber(ARGV[5])

local state = redis.call('HMGET', KEYS[1], 'units', 'ts')
local units = tonumber(state[1])
local ts = tonumber(state[2])
if units == nil or ts == nil then
  units = capacity
  ts = now
end

-- Clocks of gateway instances may disagree slightly; never refill backwards.
if now > ts then
  units = math.min(capacity, units + (now - ts) * per_minute)
  ts = now
end

local allowed = 0
local retry = 0
if units >= unit then
  units = units - unit
  allowed = 1
else
  retry = math.ceil((unit - units) / per_minute)
end

-- Units are whole numbers; %.0f keeps them exact where tostring would round
-- to 14 significant digits.
redis.call('HSET', KEYS[1], 'units', string.format('%.0f', units), 'ts', string.format('%.0f', ts))
redis.call('PEXPIRE', KEYS[1], ttl)
return {allowed, math.floor(units / unit), retry}
`)

// Result is the outcome of a single rate limit check.
type Result struct {
	Allowed    bool
	Limit      int
	Remaining  int
	RetryAfter time.Duration
}

// RedisLimiter is a token bucket: each key may spend up to burst requests at
// once, and its budget refills at perMinute requests per minute.
type RedisLimiter struct {
	client    redis.Scripter
	perMinute int64
	burst     int64
	ttl       time.Duration
	now       func() time.Time
}

// NewRedisLimiter creates a limiter on top of the given Redis client.
func NewRedisLimiter(client redis.Scripter, perMinute, burst uint32) (*RedisLimiter, error) {
	if perMinute == 0 || burst == 0 {
		return nil, errors.New("rate limit and burst must be positive")
	}

	// An idle bucket is full again after burst/perMinute minutes; past that
	// point it is equivalent to a missing one, so let Redis drop it.
	refill := time.Duration(int64(burst)) * time.Minute / time.Duration(int64(perMinute))
	return &RedisLimiter{
		client:    client,
		perMinute: int64(perMinute),
		burst:     int64(burst),
		ttl:       refill + time.Second,
		now:       time.Now,
	}, nil
}

// Allow takes one token from the bucket of key.
func (l *RedisLimiter) Allow(ctx context.Context, key string) (Result, error) {
	values, err := tokenBucket.Run(ctx, l.client, []string{keyPrefix + key},
		l.perMinute,
		l.burst*unitsPerToken,
		l.now().UnixMilli(),
		l.ttl.Milliseconds(),
		unitsPerToken,
	).Int64Slice()
	if err != nil {
		return Result{}, fmt.Errorf("failed to run token bucket for %s: %w", key, err)
	}
	if len(values) != 3 {
		return Result{}, fmt.Errorf("unexpected token bucket reply of %d values", len(values))
	}

	return Result{
		Allowed:    values[0] == 1,
		Limit:      int(l.burst),
		Remaining:  int(values[1]),
		RetryAfter: time.Duration(values[2]) * time.Millisecond,
	}, nil
}
