package middleware

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
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

// Refunder returns a token that Allow took. Limiters without it are never
// refunded.
type Refunder interface {
	Refund(ctx context.Context, key string) error
}

// LimitOption adjusts RateLimit and RateLimitRequired.
type LimitOption func(*limitOptions)

type limitOptions struct{ refund string }

// Refundable records the token a request takes under name, so that Refund
// can give it back once the request proves a credential (docs/gateway/rate-limits.md).
func Refundable(name string) LimitOption {
	return func(o *limitOptions) { o.refund = name }
}

// RateLimit rejects requests over the limiter's quota with 429. When the
// limiter itself fails (Redis is down), requests are let through: losing rate
// limiting is preferable to losing the whole API (ARCHITECTURE.md 6.2). A
// request whose deadline passed while waiting for the limiter is answered
// with 504 instead, since nothing may run after its budget is spent.
func RateLimit(limiter Limiter, key KeyFunc, logger *slog.Logger, opts ...LimitOption) func(http.Handler) http.Handler {
	return rateLimit(limiter, key, logger, false, opts)
}

// RateLimitRequired refuses credential requests when the shared limiter is
// unavailable. Ordinary authenticated API traffic can still fail open.
func RateLimitRequired(limiter Limiter, key KeyFunc, logger *slog.Logger, opts ...LimitOption) func(http.Handler) http.Handler {
	return rateLimit(limiter, key, logger, true, opts)
}

func rateLimit(limiter Limiter, key KeyFunc, logger *slog.Logger, required bool, opts []LimitOption) func(http.Handler) http.Handler {
	var o limitOptions
	for _, opt := range opts {
		opt(&o)
	}
	refunder, _ := limiter.(Refunder)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k := key(r)
			result, err := limiter.Allow(r.Context(), k)
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
			if o.refund != "" && refunder != nil {
				r = withCharge(r, o.refund, charge{refunder, k})
			}
			next.ServeHTTP(w, r)
		})
	}
}

// A charge is a token one request took from a refundable limiter.
type charge struct {
	limiter Refunder
	key     string
}

// charges are the refundable tokens of one request, by Refundable name.
type charges struct {
	mu     sync.Mutex
	byName map[string][]charge
}

type chargesKey struct{}

func withCharge(r *http.Request, name string, c charge) *http.Request {
	held, ok := r.Context().Value(chargesKey{}).(*charges)
	if !ok {
		held = &charges{byName: map[string][]charge{}}
		r = r.WithContext(context.WithValue(r.Context(), chargesKey{}, held))
	}
	held.mu.Lock()
	held.byName[name] = append(held.byName[name], c)
	held.mu.Unlock()
	return r
}

// refundTimeout bounds a refund, which also runs after the request budget
// is spent; it matches the Redis client timeouts.
const refundTimeout = 250 * time.Millisecond

// Refund gives back the tokens the request took under each name, at most
// once. A failed refund only leaves the client with less quota.
func Refund(r *http.Request, logger *slog.Logger, names ...string) {
	held, ok := r.Context().Value(chargesKey{}).(*charges)
	if !ok {
		return
	}
	var due []charge
	held.mu.Lock()
	for _, name := range names {
		due = append(due, held.byName[name]...)
		delete(held.byName, name)
	}
	held.mu.Unlock()
	if len(due) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), refundTimeout)
	defer cancel()
	for _, c := range due {
		if err := c.limiter.Refund(ctx, c.key); err != nil {
			logger.WarnContext(ctx, "rate limit refund failed", "error", err)
		}
	}
}

// RefundAuthenticated refunds names for requests that reach it, so place it
// after Authenticate: holders of a valid access token are limited per user.
func RefundAuthenticated(logger *slog.Logger, names ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Refund(r, logger, names...)
			next.ServeHTTP(w, r)
		})
	}
}

// RefundOnStatus refunds names when the handler answers with status, which
// proves the request carried a valid credential.
func RefundOnStatus(status int, logger *slog.Logger, names ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			if rec.status == status || rec.status == 0 && status == http.StatusOK {
				Refund(r, logger, names...)
			}
		})
	}
}
