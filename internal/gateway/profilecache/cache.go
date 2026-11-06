// Package profilecache keeps public profiles {user_id, username} in a Redis
// instance of their own (docs/adr/0004-public-profile-cache.md). The cache
// only saves GetUser calls: callers authenticate the request first, and any
// cache failure is a miss, so the response never depends on the cache.
package profilecache

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	// FoundTTL bounds how long a profile is served after it changed or its
	// account was deleted; usernames are immutable today.
	FoundTTL = 10 * time.Minute
	// MissingTTL is the lifetime of an answer that no such account exists.
	MissingTTL = time.Minute
	// Timeout bounds every cache operation; slower answers count as misses.
	Timeout = 250 * time.Millisecond

	keyPrefix = "cache:profile:v1:"
	maxValue  = 128
)

// Profile is the public projection of an account. An empty Username records
// that no account has UserID.
type Profile struct {
	UserID   string `json:"user_id"`
	Username string `json:"username,omitempty"`
}

// Cache reads and writes profiles. Its methods are safe for concurrent use.
type Cache struct {
	client                 *redis.Client
	hits, misses, failures atomic.Uint64
}

// New wraps a client of the profile cache instance; it must not be the
// Redis that holds rate limits and events.
func New(client *redis.Client) *Cache {
	return &Cache{client: client}
}

// Cacheable reports whether userID is a canonical UUID, the only form used
// as a key: other spellings go to Auth, so one account has one key and keys
// stay short.
func Cacheable(userID string) bool {
	id, err := uuid.Parse(userID)
	return err == nil && id != uuid.Nil && id.String() == userID
}

// Get returns the cached profile of a cacheable userID. A missing,
// malformed or unreadable value is a miss.
func (c *Cache) Get(ctx context.Context, userID string) (Profile, bool) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	data, err := c.client.Get(ctx, keyPrefix+userID).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			c.misses.Add(1)
		} else {
			c.failures.Add(1)
		}
		return Profile{}, false
	}
	var p Profile
	if len(data) > maxValue || json.Unmarshal(data, &p) != nil || p.UserID != userID {
		c.misses.Add(1)
		return Profile{}, false
	}
	c.hits.Add(1)
	return p, true
}

// Put stores p for FoundTTL, or for MissingTTL when it records a missing
// account. Every write sets the TTL, so a stale value written after an
// operator deleted the key disappears within it. Failures are only counted.
func (c *Cache) Put(ctx context.Context, p Profile) {
	data, err := json.Marshal(p)
	if err != nil || len(data) > maxValue || !Cacheable(p.UserID) {
		return
	}
	ttl := FoundTTL
	if p.Username == "" {
		ttl = MissingTTL
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	if c.client.Set(ctx, keyPrefix+p.UserID, data, ttl).Err() != nil {
		c.failures.Add(1)
	}
}

// Counts returns the lookups that hit, missed, or failed to reach the cache,
// and failed writes, which count as errors too.
func (c *Cache) Counts() (hits, misses, errs uint64) {
	return c.hits.Load(), c.misses.Load(), c.failures.Load()
}
