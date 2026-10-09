package ratelimit_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/ratelimit"
)

// startHungRedis accepts TCP connections and never answers, like a Redis
// that is reachable but stuck.
func startHungRedis(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		var conns []net.Conn
		defer func() {
			for _, conn := range conns {
				_ = conn.Close()
			}
		}()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conns = append(conns, conn)
		}
	}()
	return listener.Addr().String()
}

func TestNewRedisClient_HonorsRequestDeadline(t *testing.T) {
	client := ratelimit.NewRedisClient(startHungRedis(t), "")
	t.Cleanup(func() { _ = client.Close() })
	limiter, err := ratelimit.NewRedisLimiter(client, 60, 1)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = limiter.Allow(ctx, "ip:203.0.113.7")
	elapsed := time.Since(start)

	require.Error(t, err)
	// The client's own I/O timeouts are 250ms; returning well before that
	// proves the request deadline, not the client default, stopped the call.
	require.Less(t, elapsed, 200*time.Millisecond, "Redis I/O must stop at the request deadline")
}
