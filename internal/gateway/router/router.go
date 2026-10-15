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
)

// Deps are the collaborators and settings the routes are built from.
type Deps struct {
	Auth               *handler.AuthHandler
	Chat               *handler.ChatHandler
	Health             *handler.Health
	TokenValidator     middleware.TokenValidator
	Limiter            middleware.Limiter
	Logger             *slog.Logger
	CORSAllowedOrigins []string
	MaxBodyBytes       int64
	RequestTimeout     time.Duration
	// Realtime serves WebSocket connections; nil disables the route.
	Realtime http.Handler
}

// New returns the gateway's root handler.
//
// Every request gets a request id, an access log line, panic recovery, and
// security headers. Probes (/health, /ready) stop there, so orchestrators
// are never rate limited. API routes (/api/) additionally run within the
// request timeout and go through CORS, per-IP rate limiting, and the body
// size limit; protected routes also require a valid access token and are
// rate limited per user. The WebSocket route is long-lived, so it bypasses
// the request timeout and body limit; its upgrade is rate limited per IP,
// and the connection authenticates in-band and checks origins itself.
func New(d Deps) http.Handler {
	protected := func(h http.HandlerFunc) http.Handler {
		return middleware.Chain(h,
			middleware.Authenticate(d.TokenValidator),
			middleware.RateLimit(d.Limiter, middleware.ByUserID, d.Logger),
		)
	}

	api := http.NewServeMux()
	api.HandleFunc("POST /api/v1/auth/register", d.Auth.Register)
	api.HandleFunc("POST /api/v1/auth/login", d.Auth.Login)
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
	api.HandleFunc("/", notFound)

	root := http.NewServeMux()
	root.HandleFunc("GET /health", d.Health.Live)
	root.HandleFunc("GET /ready", d.Health.Ready)
	if d.Realtime != nil {
		// More specific than "/api/", so the API chain does not apply.
		root.Handle("GET /api/v1/ws", middleware.Chain(d.Realtime, middleware.RateLimit(d.Limiter, middleware.ByClientIP, d.Logger)))
	}
	// Timeout comes first so that the whole request, rate limiting
	// included, runs within one budget.
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
