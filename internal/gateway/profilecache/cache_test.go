package profilecache_test

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/profilecache"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/ratelimit"
)

const (
	alice = "0b5f1f7a-6a7e-4d8e-9a43-3f2f4c1d2e10"
	key   = "cache:profile:v1:" + alice
)

func newCache(t *testing.T) (*profilecache.Cache, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := ratelimit.NewRedisClient(server.Addr(), "")
	t.Cleanup(func() { _ = client.Close() })
	return profilecache.New(client), server
}

func counts(c *profilecache.Cache) [3]uint64 {
	hits, misses, errs := c.Counts()
	return [3]uint64{hits, misses, errs}
}

func TestCacheable_OnlyCanonicalUUIDs(t *testing.T) {
	require.True(t, profilecache.Cacheable(alice))
	for _, id := range []string{
		"", "peer", "0B5F1F7A-6A7E-4D8E-9A43-3F2F4C1D2E10", "{" + alice + "}",
		"urn:uuid:" + alice, "0b5f1f7a6a7e4d8e9a433f2f4c1d2e10", "00000000-0000-0000-0000-000000000000",
	} {
		require.False(t, profilecache.Cacheable(id), id)
	}
}

// Scenarios 1, 2, 4 and 11 of the ADR: a miss, a hit, only the public
// projection in Redis, a 10-minute TTL, and counters for each lookup.
func TestCache_StoresOnlyThePublicProjectionForTenMinutes(t *testing.T) {
	c, server := newCache(t)
	ctx := context.Background()
	_, ok := c.Get(ctx, alice)
	require.False(t, ok)

	c.Put(ctx, profilecache.Profile{UserID: alice, Username: "alice"})
	p, ok := c.Get(ctx, alice)
	require.True(t, ok)
	require.Equal(t, profilecache.Profile{UserID: alice, Username: "alice"}, p)

	raw, err := server.Get(key)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &fields))
	require.Equal(t, map[string]any{"user_id": alice, "username": "alice"}, fields)
	require.Equal(t, profilecache.FoundTTL, server.TTL(key))

	server.FastForward(profilecache.FoundTTL)
	_, ok = c.Get(ctx, alice)
	require.False(t, ok, "expired after the TTL")
	require.Equal(t, [3]uint64{1, 2, 0}, counts(c))
}

// Scenario 5: a missing account is remembered for one minute only.
func TestCache_RemembersMissingAccountsForOneMinute(t *testing.T) {
	c, server := newCache(t)
	ctx := context.Background()
	c.Put(ctx, profilecache.Profile{UserID: alice})
	p, ok := c.Get(ctx, alice)
	require.True(t, ok)
	require.Empty(t, p.Username)
	require.Equal(t, profilecache.MissingTTL, server.TTL(key))
	server.FastForward(profilecache.MissingTTL)
	_, ok = c.Get(ctx, alice)
	require.False(t, ok)
}

// Scenario 7: a damaged or foreign value is a miss, and the next Put
// replaces it.
func TestCache_TreatsDamagedValuesAsMisses(t *testing.T) {
	c, server := newCache(t)
	ctx := context.Background()
	for _, value := range []string{
		"not json", `{"user_id":"` + alice, `{"user_id":"11111111-2222-4333-8444-555555555555","username":"eve"}`,
		`{"user_id":"` + alice + `","username":"` + string(make([]byte, 200)) + `"}`,
	} {
		require.NoError(t, server.Set(key, value))
		_, ok := c.Get(ctx, alice)
		require.False(t, ok, value)
	}
	c.Put(ctx, profilecache.Profile{UserID: alice, Username: "alice"})
	_, ok := c.Get(ctx, alice)
	require.True(t, ok)
}

// Scenario 6: an unreachable cache answers quickly with a counted miss, and
// writes fail silently.
func TestCache_UnreachableIsAQuickMiss(t *testing.T) {
	c, server := newCache(t)
	server.Close()
	ctx := context.Background()
	start := time.Now()
	_, ok := c.Get(ctx, alice)
	c.Put(ctx, profilecache.Profile{UserID: alice, Username: "alice"})
	require.False(t, ok)
	require.Less(t, time.Since(start), 2*time.Second)
	require.Equal(t, [3]uint64{0, 0, 2}, counts(c))
}

// The cache's own deadline holds even when the caller has none and the
// client's socket timeouts are long: a server that accepts and never answers
// costs one Timeout.
func TestCache_BoundsSlowAnswers(t *testing.T) {
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = silent.Close() })
	go func() {
		for {
			conn, err := silent.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	client := redis.NewClient(&redis.Options{Addr: silent.Addr().String(), MaxRetries: -1,
		ContextTimeoutEnabled: true, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second})
	t.Cleanup(func() { _ = client.Close() })
	c := profilecache.New(client)
	start := time.Now()
	_, ok := c.Get(context.Background(), alice)
	require.False(t, ok)
	require.Less(t, time.Since(start), 900*time.Millisecond)
}

// Scenario 10: a stale profile written after an operator's DEL still
// carries the TTL, so it disappears within it.
func TestCache_StaleWriteAfterDeleteExpires(t *testing.T) {
	c, server := newCache(t)
	ctx := context.Background()
	c.Put(ctx, profilecache.Profile{UserID: alice, Username: "alice"})
	server.Del(key)
	c.Put(ctx, profilecache.Profile{UserID: alice, Username: "alice"}) // a slow request's stale answer
	require.Equal(t, profilecache.FoundTTL, server.TTL(key))
	server.FastForward(profilecache.FoundTTL)
	require.False(t, server.Exists(key))
}

// Only canonical ids become keys, and values stay within the size bound.
func TestCache_PutSkipsUncacheableProfiles(t *testing.T) {
	c, server := newCache(t)
	ctx := context.Background()
	c.Put(ctx, profilecache.Profile{UserID: "peer", Username: "bob"})
	c.Put(ctx, profilecache.Profile{UserID: alice, Username: string(make([]byte, 200))})
	require.Empty(t, server.Keys())
}
