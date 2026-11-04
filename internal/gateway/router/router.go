// Package router defines the routes of the API gateway and the middleware
// each of them runs through.
package router

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/response"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/ratelimit"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/transfer"
)

// Deps are the collaborators and settings the routes are built from.
type Deps struct {
	Auth           *handler.AuthHandler
	Chat           *handler.ChatHandler
	Media          *handler.MediaHandler
	Health         *handler.Health
	TokenValidator middleware.TokenValidator
	Limiter        middleware.Limiter
	// AuthLimiter enforces the shared credential quota per client IP and
	// subject, AuthIPLimiter the one per client IP; both fail closed.
	// RefreshLimiter is the shared refresh quota per client IP and fails
	// open. The process-local quotas are always enforced as well. See
	// docs/gateway/rate-limits.md.
	AuthLimiter               middleware.Limiter
	AuthIPLimiter             middleware.Limiter
	RefreshLimiter            middleware.Limiter
	AuthRateLimitPerMinute    uint32
	AuthRateLimitBurst        uint32
	AuthIPRateLimitPerMinute  uint32
	AuthIPRateLimitBurst      uint32
	RefreshRateLimitPerMinute uint32
	RefreshRateLimitBurst     uint32
	Logger                    *slog.Logger
	CORSAllowedOrigins        []string
	MaxBodyBytes              int64
	RequestTimeout            time.Duration
	// Realtime serves WebSocket connections; nil disables the route.
	Realtime http.Handler
	// Web serves the browser client at "/" and "/app/"; nil disables it.
	Web http.Handler
}

// New returns the gateway's root handler.
//
// Every request gets a request id, an access log line, panic recovery, and
// security headers. Probes (/health, /ready) and the web client's static
// files stop there, so orchestrators and page loads are never rate limited.
// API routes (/api/) additionally run within the request timeout and go
// through CORS, per-IP rate limiting, and the body size limit; protected
// routes also require a valid access token, which returns the per-IP token,
// and are rate limited per user. Credential and refresh routes have quotas
// of their own (docs/gateway/rate-limits.md). The WebSocket route is
// long-lived, so it bypasses the request timeout and body limit; its upgrade
// is rate limited per IP, and the connection authenticates in-band and
// checks origins itself.
func New(d Deps) http.Handler {
	// Tokens taken under these names are returned once a request proves a
	// credential, so users sharing an address are limited by what they get
	// wrong, not by how many they are.
	const (
		ipCharge      = "ip"
		authIPCharge  = "auth-ip"
		refreshCharge = "refresh-ip"
	)
	clientIP := middleware.Refundable(ipCharge)
	authIPKey := func(r *http.Request) string { return "auth-ip:" + middleware.ByClientIP(r) }
	subjectKey := func(r *http.Request) string { return "auth:" + middleware.ByCredentialSubject(r) }
	refreshKey := func(r *http.Request) string { return "refresh-ip:" + middleware.ByClientIP(r) }
	authIPLocal := localLimiter(d.AuthIPRateLimitPerMinute, d.AuthIPRateLimitBurst, 10, 4096)
	subjectLocal := localLimiter(d.AuthRateLimitPerMinute, d.AuthRateLimitBurst, 5, 16384)
	refreshLocal := localLimiter(d.RefreshRateLimitPerMinute, d.RefreshRateLimitBurst, 30, 4096)

	// credentials limits a credential route per client IP and per client IP
	// and subject; success, when nonzero, is the status that proves the
	// credential and refunds the per-IP tokens.
	credentials := func(h http.HandlerFunc, success int) http.Handler {
		limits := []func(http.Handler) http.Handler{
			middleware.RateLimit(authIPLocal, authIPKey, d.Logger, middleware.Refundable(authIPCharge)),
		}
		if d.AuthIPLimiter != nil {
			limits = append(limits, middleware.RateLimitRequired(d.AuthIPLimiter, authIPKey, d.Logger, middleware.Refundable(authIPCharge)))
		}
		limits = append(limits, middleware.CaptureCredentialSubject(h), middleware.RateLimit(subjectLocal, subjectKey, d.Logger))
		if d.AuthLimiter != nil {
			limits = append(limits, middleware.RateLimitRequired(d.AuthLimiter, subjectKey, d.Logger))
		}
		if success != 0 {
			limits = append(limits, middleware.RefundOnStatus(success, d.Logger, ipCharge, authIPCharge))
		}
		return middleware.Chain(h, limits...)
	}
	refresh := func(h http.HandlerFunc) http.Handler {
		limits := []func(http.Handler) http.Handler{
			middleware.RateLimit(refreshLocal, refreshKey, d.Logger, middleware.Refundable(refreshCharge)),
		}
		if d.RefreshLimiter != nil {
			limits = append(limits, middleware.RateLimit(d.RefreshLimiter, refreshKey, d.Logger, middleware.Refundable(refreshCharge)))
		}
		limits = append(limits, middleware.RefundOnStatus(http.StatusOK, d.Logger, ipCharge, refreshCharge))
		return middleware.Chain(h, limits...)
	}
	authenticated := []func(http.Handler) http.Handler{
		middleware.Authenticate(d.TokenValidator),
		middleware.RefundAuthenticated(d.Logger, ipCharge),
		middleware.RateLimit(d.Limiter, middleware.ByUserID, d.Logger),
	}
	protected := func(h http.HandlerFunc) http.Handler {
		return middleware.Chain(h, authenticated...)
	}
	protectedCredentials := func(h http.HandlerFunc) http.Handler {
		return middleware.Chain(credentials(h, 0), authenticated...)
	}

	api := http.NewServeMux()
	// Email and recovery requests are acknowledged whatever the account,
	// so their answers prove nothing and refund nothing.
	api.Handle("POST /api/v1/auth/register", credentials(d.Auth.Register, http.StatusCreated))
	api.Handle("POST /api/v1/auth/login", credentials(d.Auth.Login, http.StatusOK))
	api.Handle("POST /api/v1/auth/email/verification/request", credentials(d.Auth.RequestEmailVerification, 0))
	api.Handle("POST /api/v1/auth/email/verification/confirm", credentials(d.Auth.ConfirmEmailVerification, 0))
	api.Handle("POST /api/v1/auth/password/reset/request", credentials(d.Auth.RequestPasswordReset, 0))
	api.Handle("POST /api/v1/auth/password/reset/confirm", credentials(d.Auth.ConfirmPasswordReset, 0))
	api.Handle("POST /api/v1/auth/email/change/request", protectedCredentials(d.Auth.RequestEmailChange))
	api.Handle("POST /api/v1/auth/email/change/confirm", credentials(d.Auth.ConfirmEmailChange, 0))
	api.Handle("GET /api/v1/auth/account", protected(d.Auth.Account))
	api.Handle("POST /api/v1/auth/refresh", refresh(d.Auth.Refresh))
	api.HandleFunc("POST /api/v1/auth/logout", d.Auth.Logout)
	api.Handle("GET /api/v1/auth/me", protected(d.Auth.Me))
	api.Handle("DELETE /api/v1/auth/sessions", protected(d.Auth.RevokeAllSessions))
	api.Handle("GET /api/v1/users", protected(d.Auth.Users))
	api.Handle("GET /api/v1/users/{user_id}", protected(d.Auth.User))
	if d.Chat != nil {
		api.Handle("POST /api/v1/rooms/{room_id}/messages", protected(d.Chat.SendMessage))
		api.Handle("GET /api/v1/rooms/{room_id}/messages", protected(d.Chat.Messages))
		api.Handle("POST /api/v1/rooms/direct", protected(d.Chat.Direct))
		api.Handle("POST /api/v1/rooms/channels", protected(d.Chat.CreateChannel))
		api.Handle("GET /api/v1/rooms", protected(d.Chat.Rooms))
		api.Handle("GET /api/v1/rooms/{room_id}", protected(d.Chat.Room))
		api.Handle("PUT /api/v1/rooms/{room_id}/members/{user_id}", protected(d.Chat.AddMember))
		api.Handle("GET /api/v1/rooms/{room_id}/members", protected(d.Chat.Members))
		api.Handle("DELETE /api/v1/rooms/{room_id}/members/{user_id}", protected(d.Chat.RemoveMember))
		// The literal segment takes precedence over {user_id}, which must be a UUID.
		api.Handle("DELETE /api/v1/rooms/{room_id}/members/me", protected(d.Chat.LeaveRoom))
	}
	if d.Media != nil {
		api.Handle("GET /api/v1/files/{file_id}", protected(d.Media.File))
	}
	api.HandleFunc("/", notFound)

	root := http.NewServeMux()
	root.HandleFunc("GET /health", d.Health.Live)
	root.HandleFunc("GET /ready", d.Health.Ready)
	if d.Web != nil {
		// Static files of the client: no rate limit or request budget, like
		// the probes; the API it calls has its own.
		root.Handle("GET /{$}", d.Web)
		root.Handle("GET /app/", d.Web)
	}
	if d.Realtime != nil {
		// More specific than "/api/", so the API chain does not apply.
		root.Handle("GET /api/v1/ws", middleware.Chain(d.Realtime, middleware.RateLimit(d.Limiter, middleware.ByClientIP, d.Logger)))
	}
	// Timeout comes first so that the whole request, rate limiting
	// included, runs within one budget.
	if d.Media != nil {
		root.Handle("POST /api/v1/rooms/{room_id}/files", middleware.Chain(protected(d.Media.Upload),
			middleware.FileTransfer(transfer.MaxDuration, transfer.IdleTimeout), middleware.CORS(d.CORSAllowedOrigins),
			middleware.RateLimit(d.Limiter, middleware.ByClientIP, d.Logger, clientIP)))
		root.Handle("GET /api/v1/files/{file_id}/content", middleware.Chain(protected(d.Media.Download),
			middleware.FileTransfer(transfer.MaxDuration, transfer.IdleTimeout), middleware.CORS(d.CORSAllowedOrigins),
			middleware.RateLimit(d.Limiter, middleware.ByClientIP, d.Logger, clientIP)))
	}
	root.Handle("/api/", middleware.Chain(api,
		middleware.Timeout(d.RequestTimeout),
		middleware.CORS(d.CORSAllowedOrigins),
		middleware.RateLimit(d.Limiter, middleware.ByClientIP, d.Logger, clientIP),
		middleware.BodyLimit(d.MaxBodyBytes),
	))
	root.HandleFunc("/", notFound)

	return middleware.Chain(root,
		middleware.RequestID,
		middleware.Logging(d.Logger),
		middleware.Recover(d.Logger),
		middleware.SecurityHeaders,
	)
}

// localLimiter is a process-local bucket; zero settings take the defaults.
func localLimiter(perMinute, burst uint32, fallback uint32, maxKeys int) *ratelimit.MemoryLimiter {
	if perMinute == 0 {
		perMinute = fallback
	}
	if burst == 0 {
		burst = fallback
	}
	return ratelimit.NewMemoryLimiter(time.Minute/time.Duration(perMinute), int(burst), maxKeys)
}

func notFound(w http.ResponseWriter, _ *http.Request) {
	response.Error(w, http.StatusNotFound, "not_found", "route not found")
}
