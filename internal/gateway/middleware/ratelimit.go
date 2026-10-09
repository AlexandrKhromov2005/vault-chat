package middleware

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/ratelimit"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/response"
)

// Limiter decides whether the client identified by key may make another
// request. Defined at the consumer per CONTRIBUTING.md.
type Limiter interface {
	Allow(ctx context.Context, key string) (ratelimit.Result, error)
}

// KeyFunc identifies the client a request is counted against.
type KeyFunc func(r *http.Request) string

// ByClientIP counts requests per client IP address.
func ByClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "ip:" + host
}

// ByUserID counts requests per authenticated user and falls back to the
// client IP for anonymous requests. Place it after Authenticate.
func ByUserID(r *http.Request) string {
	if identity, ok := IdentityFromContext(r.Context()); ok {
		return "user:" + identity.UserID
	}
	return ByClientIP(r)
}

// RateLimit rejects requests over the limiter's quota with 429. When the
// limiter itself fails (Redis is down), requests are let through: losing rate
// limiting is preferable to losing the whole API (ARCHITECTURE.md 6.2).
func RateLimit(limiter Limiter, key KeyFunc, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			result, err := limiter.Allow(r.Context(), key(r))
			if err != nil {
				logger.WarnContext(r.Context(), "rate limiter unavailable, allowing request", "error", err)
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(result.Limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))
			if !result.Allowed {
				retryAfter := max((result.RetryAfter+time.Second-1)/time.Second, 1)
				w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter)))
				response.Error(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
