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
func NewRedisClient(addr, password string) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:                  addr,
		Password:              password,
		ContextTimeoutEnabled: true,
		DialTimeout:           250 * time.Millisecond,
		ReadTimeout:           250 * time.Millisecond,
		WriteTimeout:          250 * time.Millisecond,
		PoolTimeout:           500 * time.Millisecond,
	})
}
