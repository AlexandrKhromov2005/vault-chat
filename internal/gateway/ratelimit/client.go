package ratelimit

import (
	"time"

	"github.com/redis/go-redis/v9"
)

// NewRedisClient returns the Redis client the gateway's limiter runs on.
//
// ContextTimeoutEnabled makes every command, its retries, and waits for a
// pooled connection stop at the request deadline, so rate limiting stays
// inside GATEWAY_REQUEST_TIMEOUT. The short client timeouts are the bound for
// calls without a deadline; the limiter fails open, so an unreachable Redis
// must not add seconds to every request.
//
// For the same reason there are no retries: go-redis would otherwise dial up
// to five times per attempt and retry each command three times, so a down
// Redis (or one whose name no longer resolves, as when its container is
// stopped) would delay every request by seconds before it is let through.
func NewRedisClient(addr, password string) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:                  addr,
		Password:              password,
		ContextTimeoutEnabled: true,
		MaxRetries:            -1,
		DialerRetries:         1,
		DialTimeout:           250 * time.Millisecond,
		ReadTimeout:           250 * time.Millisecond,
		WriteTimeout:          250 * time.Millisecond,
		PoolTimeout:           500 * time.Millisecond,
	})
}
