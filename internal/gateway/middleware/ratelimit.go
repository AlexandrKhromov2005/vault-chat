package middleware

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
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

// ByClientIP counts IPv4 addresses and IPv6 /64 networks. Canonicalization
// prevents bypasses using IPv4-mapped IPv6 or alternate IPv6 spellings.
func ByClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		addr = addr.Unmap().WithZone("")
		host = addr.String()
		if addr.Is6() {
			host = netip.PrefixFrom(addr, 64).Masked().String()
		}
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
// limiting is preferable to losing the whole API (ARCHITECTURE.md 6.2). A
// request whose deadline passed while waiting for the limiter is answered
// with 504 instead, since nothing may run after its budget is spent.
func RateLimit(limiter Limiter, key KeyFunc, logger *slog.Logger) func(http.Handler) http.Handler {
	return rateLimit(limiter, key, logger, false)
}

// RateLimitRequired refuses credential requests when the shared limiter is
// unavailable. Ordinary authenticated API traffic can still fail open.
func RateLimitRequired(limiter Limiter, key KeyFunc, logger *slog.Logger) func(http.Handler) http.Handler {
	return rateLimit(limiter, key, logger, true)
}

func rateLimit(limiter Limiter, key KeyFunc, logger *slog.Logger, required bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			result, err := limiter.Allow(r.Context(), key(r))
			if err != nil && r.Context().Err() != nil {
				response.Error(w, http.StatusGatewayTimeout, "timeout", "request timed out")
				return
			}
			if err != nil {
				if required {
					logger.WarnContext(r.Context(), "credential rate limiter unavailable", "error", err)
					response.Error(w, http.StatusServiceUnavailable, "unavailable", "authentication temporarily unavailable")
					return
				}
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
