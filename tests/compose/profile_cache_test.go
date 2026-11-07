//go:build compose

package compose

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// composeCommand runs docker compose on the stack under test;
// VAULT_CHAT_COMPOSE_PROJECT names the project when it is not the default.
func composeCommand(args ...string) *exec.Cmd {
	base := []string{"compose", "-f", "../../deployments/docker-compose.yml"}
	if project := os.Getenv("VAULT_CHAT_COMPOSE_PROJECT"); project != "" {
		base = append(base, "-p", project)
	}
	return exec.Command("docker", append(base, args...)...)
}

func compose(t *testing.T, args ...string) string {
	t.Helper()
	out, err := composeCommand(args...).CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.TrimSpace(string(out))
}

func redisCLI(t *testing.T, service string, args ...string) string {
	t.Helper()
	return compose(t, append([]string{"exec", "-T", service, "redis-cli"}, args...)...)
}

func profile(t *testing.T, api, token, id string) (int, map[string]any) {
	t.Helper()
	code, body := fileRequest(t, http.MethodGet, api+"/users/"+id, token)
	var out map[string]any
	require.NoError(t, json.Unmarshal(body, &out), string(body))
	return code, out
}

// ADR 0004 on the real stack: profiles are cached in their own Redis instance,
// served from it, survive its outage through Auth, and filling it evicts only
// its own keys. The test stops and fills profile-cache, so run it only against
// a disposable stack.
func TestCompose_ProfileCache(t *testing.T) {
	base := gatewayURL(t)
	waitReady(t, base)
	api := base + "/api/v1"
	suffix := uuid.NewString()[:8]
	_, token := signUp(t, api, "cachea"+suffix)
	bobID, _ := signUp(t, api, "cacheb"+suffix)
	key := "cache:profile:v1:" + bobID

	code, body := profile(t, api, token, bobID)
	require.Equal(t, http.StatusOK, code, body)
	require.Equal(t, "cacheb"+suffix, body["username"])

	// Only the public projection, with the TTL, and only in the cache instance.
	var stored map[string]any
	require.NoError(t, json.Unmarshal([]byte(redisCLI(t, "profile-cache", "GET", key)), &stored))
	require.Equal(t, map[string]any{"user_id": bobID, "username": "cacheb" + suffix}, stored)
	ttl, err := strconv.Atoi(redisCLI(t, "profile-cache", "TTL", key))
	require.NoError(t, err)
	require.True(t, ttl > 0 && ttl <= 600, "TTL %d", ttl)
	require.Equal(t, "0", redisCLI(t, "redis", "EXISTS", key))

	// A changed value in the cache is what the gateway answers: a hit.
	redisCLI(t, "profile-cache", "SET", key, `{"user_id":"`+bobID+`","username":"from-cache"}`, "EX", "60")
	_, body = profile(t, api, token, bobID)
	require.Equal(t, "from-cache", body["username"])
	redisCLI(t, "profile-cache", "DEL", key)

	// Without the cache the profile comes from Auth, quickly.
	compose(t, "stop", "profile-cache")
	t.Cleanup(func() { _ = composeCommand("start", "profile-cache").Run() })
	start := time.Now()
	code, body = profile(t, api, token, bobID)
	require.Equal(t, http.StatusOK, code, body)
	require.Equal(t, "cacheb"+suffix, body["username"])
	require.Less(t, time.Since(start), 2*time.Second)
	compose(t, "start", "profile-cache")
	require.Eventually(t, func() bool { return composeCommand("exec", "-T", "profile-cache", "redis-cli", "PING").Run() == nil },
		30*time.Second, 500*time.Millisecond)

	// Filling the cache past maxmemory evicts its own keys and nothing in the
	// shared Redis of rate limits and events, marked here by a key without TTL.
	marker := "acceptance:profile-cache:" + suffix
	redisCLI(t, "redis", "SET", marker, "kept")
	t.Cleanup(func() { _ = composeCommand("exec", "-T", "redis", "redis-cli", "DEL", marker).Run() })
	// 2 MB per command: Redis evicts between commands, not inside a script.
	for batch := range 12 {
		redisCLI(t, "profile-cache", "EVAL", "for i = 1, 250 do redis.call('SET', 'fill:' .. ARGV[1] .. ':' .. i, string.rep('x', 8192)) end",
			"0", strconv.Itoa(batch))
	}
	evicted := -1
	for _, line := range strings.Split(redisCLI(t, "profile-cache", "INFO", "stats"), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "evicted_keys:"); ok {
			evicted, _ = strconv.Atoi(v)
		}
	}
	require.Positive(t, evicted, "24 MB into a 16 MB instance evicts")
	require.Equal(t, "kept", redisCLI(t, "redis", "GET", marker))
	code, _ = profile(t, api, token, bobID)
	require.Equal(t, http.StatusOK, code)
}
