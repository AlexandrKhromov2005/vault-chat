// Package middleware contains the HTTP middleware of the API gateway:
// correlation ids, logging, panic recovery, security headers, CORS, request
// limits, authentication, and rate limiting.
package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/response"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/requestid"
)

// Chain wraps h in middlewares; the first middleware is the outermost.
func Chain(h http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

// RequestID assigns every request a correlation id: the client's
// X-Request-ID when it is safe to reuse, a fresh one otherwise. The id is
// echoed in the response and stored in the request context.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestid.Header)
		if !requestid.Valid(id) {
			id = requestid.New()
		}
		w.Header().Set(requestid.Header, id)
		next.ServeHTTP(w, r.WithContext(requestid.NewContext(r.Context(), id)))
	})
}

// statusRecorder captures the status code written by the next handler.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

// Logging writes one structured log line per request. Only the path is
// logged, never the query string or headers, so tokens cannot leak into logs.
func Logging(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)

			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			level := slog.LevelInfo
			if status >= http.StatusInternalServerError {
				level = slog.LevelError
			}
			remoteIP, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				remoteIP = r.RemoteAddr
			}
			logger.LogAttrs(r.Context(), level, "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.String("request_id", requestid.FromContext(r.Context())),
				slog.String("remote_ip", remoteIP),
			)
		})
	}
}

// Recover turns a panicking handler into a 500 response instead of a dropped
// connection. http.ErrAbortHandler is re-raised: it is the standard way to
// abort a response on purpose.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				v := recover()
				if v == nil {
					return
				}
				if v == http.ErrAbortHandler {
					panic(v)
				}
				logger.ErrorContext(r.Context(), "panic recovered",
					"panic", fmt.Sprint(v),
					"request_id", requestid.FromContext(r.Context()),
					"stack", string(debug.Stack()),
				)
				response.Error(w, http.StatusInternalServerError, "internal", "internal error")
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeaders sets defensive response headers. API responses may carry
// tokens, so they are never cached; HSTS is only meaningful over TLS.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

const (
	corsAllowMethods  = "GET, POST, PUT, PATCH, DELETE"
	corsAllowHeaders  = "Authorization, Content-Type, X-Request-ID"
	corsExposeHeaders = "Retry-After, X-Request-ID, X-RateLimit-Limit, X-RateLimit-Remaining"
	corsMaxAgeSeconds = "600"
)

// CORS allows browsers on the given origins to call the API. A single "*"
// allows any origin. Credentials (cookies) are never allowed: the API is
// authenticated with bearer tokens only.
func CORS(origins []string) func(http.Handler) http.Handler {
	allowAll := len(origins) == 1 && origins[0] == "*"
	allowed := make(map[string]bool, len(origins))
	for _, origin := range origins {
		allowed[origin] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			h := w.Header()
			h.Add("Vary", "Origin")
			ok := allowAll || allowed[origin]
			allowOrigin := origin
			if allowAll {
				allowOrigin = "*"
			}

			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Add("Vary", "Access-Control-Request-Method")
				h.Add("Vary", "Access-Control-Request-Headers")
				if !ok {
					response.Error(w, http.StatusForbidden, "cors_forbidden", "origin not allowed")
					return
				}
				h.Set("Access-Control-Allow-Origin", allowOrigin)
				h.Set("Access-Control-Allow-Methods", corsAllowMethods)
				h.Set("Access-Control-Allow-Headers", corsAllowHeaders)
				h.Set("Access-Control-Max-Age", corsMaxAgeSeconds)
				w.WriteHeader(http.StatusNoContent)
				return
			}

			if ok {
				h.Set("Access-Control-Allow-Origin", allowOrigin)
				h.Set("Access-Control-Expose-Headers", corsExposeHeaders)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// BodyLimit caps request bodies at limit bytes. Bodies that declare a larger
// Content-Length are rejected upfront; streamed bodies fail with
// *http.MaxBytesError once the handler reads past the limit.
func BodyLimit(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > limit {
				response.Error(w, http.StatusRequestEntityTooLarge, "body_too_large",
					fmt.Sprintf("request body exceeds %d bytes", limit))
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}

// Timeout bounds the time spent on a request, including backend calls, which
// inherit the deadline through the request context.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
