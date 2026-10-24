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
	// AuthLimiter enforces the shared credential quota and fails closed.
	// The process-local credential quota is always enforced as well.
	AuthLimiter            middleware.Limiter
	AuthRateLimitPerMinute uint32
	AuthRateLimitBurst     uint32
	Logger                 *slog.Logger
	CORSAllowedOrigins     []string
	MaxBodyBytes           int64
	RequestTimeout         time.Duration
	// Realtime serves WebSocket connections; nil disables the route.
	Realtime http.Handler
	// Web serves the browser client at "/" and "/app/"; nil disables it.
	Web http.Handler
}

// New returns the gateway's root handler.
//
// Every request gets a request id, an access log line, panic recovery, and
// security headers. Probes (/health, /ready) and the web client's static
// files stop there, so orchestrators and page loads are never rate limited. API routes (/api/) additionally run within the
// request timeout and go through CORS, per-IP rate limiting, and the body
// size limit; protected routes also require a valid access token and are
// rate limited per user. The WebSocket route is long-lived, so it bypasses
// the request timeout and body limit; its upgrade is rate limited per IP,
// and the connection authenticates in-band and checks origins itself.
func New(d Deps) http.Handler {
	perMinute, burst := d.AuthRateLimitPerMinute, d.AuthRateLimitBurst
	if perMinute == 0 {
		perMinute = 5
	}
	if burst == 0 {
		burst = 5
	}
	credentialLimiter := ratelimit.NewMemoryLimiter(time.Minute/time.Duration(perMinute), int(burst), 4096)
	credentialKey := func(r *http.Request) string { return "auth:" + middleware.ByClientIP(r) }
	credentials := func(h http.HandlerFunc) http.Handler {
		limits := []func(http.Handler) http.Handler{
			middleware.RateLimit(credentialLimiter, credentialKey, d.Logger),
		}
		if d.AuthLimiter != nil {
			limits = append(limits, middleware.RateLimitRequired(d.AuthLimiter, credentialKey, d.Logger))
		}
		return middleware.Chain(h, limits...)
	}
	protected := func(h http.HandlerFunc) http.Handler {
		return middleware.Chain(h,
			middleware.Authenticate(d.TokenValidator),
			middleware.RateLimit(d.Limiter, middleware.ByUserID, d.Logger),
		)
	}

	api := http.NewServeMux()
	api.Handle("POST /api/v1/auth/register", credentials(d.Auth.Register))
	api.Handle("POST /api/v1/auth/login", credentials(d.Auth.Login))
	api.HandleFunc("POST /api/v1/auth/refresh", d.Auth.Refresh)
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
			middleware.RateLimit(d.Limiter, middleware.ByClientIP, d.Logger)))
		root.Handle("GET /api/v1/files/{file_id}/content", middleware.Chain(protected(d.Media.Download),
			middleware.FileTransfer(transfer.MaxDuration, transfer.IdleTimeout), middleware.CORS(d.CORSAllowedOrigins),
			middleware.RateLimit(d.Limiter, middleware.ByClientIP, d.Logger)))
	}
	root.Handle("/api/", middleware.Chain(api,
		middleware.Timeout(d.RequestTimeout),
		middleware.CORS(d.CORSAllowedOrigins),
		middleware.RateLimit(d.Limiter, middleware.ByClientIP, d.Logger),
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

func notFound(w http.ResponseWriter, _ *http.Request) {
	response.Error(w, http.StatusNotFound, "not_found", "route not found")
}
