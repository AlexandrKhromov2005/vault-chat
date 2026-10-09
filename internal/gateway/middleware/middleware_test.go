package middleware_test

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/requestid"
)

// newLogger returns a JSON logger writing into the returned buffer.
func newLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

// logEntries decodes every JSON log line in buf.
func logEntries(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var entries []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		entries = append(entries, entry)
	}
	return entries
}

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestChain(t *testing.T) {
	var order []string
	mark := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	h := middleware.Chain(okHandler, mark("outer"), mark("inner"))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	require.Equal(t, []string{"outer", "inner"}, order)
}

func TestRequestID(t *testing.T) {
	serve := func(incoming string) (header, inContext string) {
		h := middleware.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			inContext = requestid.FromContext(r.Context())
		}))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if incoming != "" {
			req.Header.Set(requestid.Header, incoming)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Header().Get(requestid.Header), inContext
	}

	t.Run("generates an id when absent", func(t *testing.T) {
		header, inContext := serve("")
		require.True(t, requestid.Valid(header))
		require.Equal(t, header, inContext)
	})

	t.Run("keeps a valid client id", func(t *testing.T) {
		header, inContext := serve("client-req-1")
		require.Equal(t, "client-req-1", header)
		require.Equal(t, "client-req-1", inContext)
	})

	t.Run("replaces an unsafe client id", func(t *testing.T) {
		header, inContext := serve("bad id with spaces")
		require.NotEqual(t, "bad id with spaces", header)
		require.True(t, requestid.Valid(header))
		require.Equal(t, header, inContext)
	})
}

func TestLogging(t *testing.T) {
	t.Run("logs one line per request", func(t *testing.T) {
		logger, buf := newLogger()
		h := middleware.RequestID(middleware.Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		})))

		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login?token=secret", nil)
		req.RemoteAddr = "203.0.113.7:5555"
		req.Header.Set(requestid.Header, "req-1")
		h.ServeHTTP(httptest.NewRecorder(), req)

		entries := logEntries(t, buf)
		require.Len(t, entries, 1)
		entry := entries[0]
		require.Equal(t, "INFO", entry["level"])
		require.Equal(t, "POST", entry["method"])
		require.Equal(t, "/api/v1/auth/login", entry["path"])
		require.EqualValues(t, http.StatusTeapot, entry["status"])
		require.Equal(t, "req-1", entry["request_id"])
		require.Equal(t, "203.0.113.7", entry["remote_ip"])
		require.Contains(t, entry, "duration_ms")
		require.NotContains(t, buf.String(), "secret")
	})

	t.Run("implicit 200", func(t *testing.T) {
		logger, buf := newLogger()
		h := middleware.Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ok"))
		}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

		require.EqualValues(t, http.StatusOK, logEntries(t, buf)[0]["status"])
	})

	t.Run("server errors are logged at error level", func(t *testing.T) {
		logger, buf := newLogger()
		h := middleware.Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

		require.Equal(t, "ERROR", logEntries(t, buf)[0]["level"])
	})

	t.Run("response writer stays unwrappable", func(t *testing.T) {
		logger, _ := newLogger()
		h := middleware.Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			require.NoError(t, http.NewResponseController(w).Flush())
		}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	})
}

func TestRecover(t *testing.T) {
	t.Run("panic becomes 500", func(t *testing.T) {
		logger, buf := newLogger()
		h := middleware.Recover(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("nil map write")
		}))

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		require.Equal(t, http.StatusInternalServerError, rec.Code)
		require.JSONEq(t, `{"error":{"code":"internal","message":"internal error"}}`, rec.Body.String())
		entries := logEntries(t, buf)
		require.Len(t, entries, 1)
		require.Equal(t, "ERROR", entries[0]["level"])
		require.Equal(t, "nil map write", entries[0]["panic"])
	})

	t.Run("http.ErrAbortHandler is re-raised", func(t *testing.T) {
		logger, _ := newLogger()
		h := middleware.Recover(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic(http.ErrAbortHandler)
		}))

		require.PanicsWithValue(t, http.ErrAbortHandler, func() {
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		})
	})
}

func TestSecurityHeaders(t *testing.T) {
	t.Run("plain HTTP", func(t *testing.T) {
		rec := httptest.NewRecorder()
		middleware.SecurityHeaders(okHandler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
		require.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
		require.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"))
		require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
		require.Empty(t, rec.Header().Get("Strict-Transport-Security"))
	})

	t.Run("TLS adds HSTS", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.TLS = &tls.ConnectionState{}
		rec := httptest.NewRecorder()
		middleware.SecurityHeaders(okHandler).ServeHTTP(rec, req)

		require.Equal(t, "max-age=63072000; includeSubDomains", rec.Header().Get("Strict-Transport-Security"))
	})
}

func TestCORS(t *testing.T) {
	const allowed = "https://chat.example.com"

	serve := func(origins []string, method, origin string, preflight bool) (*httptest.ResponseRecorder, bool) {
		called := false
		h := middleware.CORS(origins)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest(method, "/api/v1/auth/me", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if preflight {
			req.Header.Set("Access-Control-Request-Method", http.MethodPost)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec, called
	}

	t.Run("same-origin request is untouched", func(t *testing.T) {
		rec, called := serve([]string{allowed}, http.MethodGet, "", false)
		require.True(t, called)
		require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("allowed origin", func(t *testing.T) {
		rec, called := serve([]string{allowed}, http.MethodGet, allowed, false)
		require.True(t, called)
		require.Equal(t, allowed, rec.Header().Get("Access-Control-Allow-Origin"))
		require.Contains(t, rec.Header().Values("Vary"), "Origin")
		require.Contains(t, rec.Header().Get("Access-Control-Expose-Headers"), "Retry-After")
		require.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"))
	})

	t.Run("foreign origin gets no CORS headers", func(t *testing.T) {
		rec, called := serve([]string{allowed}, http.MethodGet, "https://evil.example", false)
		require.True(t, called)
		require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
		require.Contains(t, rec.Header().Values("Vary"), "Origin")
	})

	t.Run("preflight from allowed origin", func(t *testing.T) {
		rec, called := serve([]string{allowed}, http.MethodOptions, allowed, true)
		require.False(t, called)
		require.Equal(t, http.StatusNoContent, rec.Code)
		require.Equal(t, allowed, rec.Header().Get("Access-Control-Allow-Origin"))
		require.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), http.MethodPost)
		require.Contains(t, rec.Header().Get("Access-Control-Allow-Headers"), "Authorization")
		require.Equal(t, "600", rec.Header().Get("Access-Control-Max-Age"))
	})

	t.Run("preflight from foreign origin is rejected", func(t *testing.T) {
		rec, called := serve([]string{allowed}, http.MethodOptions, "https://evil.example", true)
		require.False(t, called)
		require.Equal(t, http.StatusForbidden, rec.Code)
		require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("CORS disabled when no origins are configured", func(t *testing.T) {
		rec, called := serve(nil, http.MethodOptions, allowed, true)
		require.False(t, called)
		require.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("OPTIONS without preflight headers is a normal request", func(t *testing.T) {
		_, called := serve([]string{allowed}, http.MethodOptions, allowed, false)
		require.True(t, called)
	})

	t.Run("wildcard", func(t *testing.T) {
		rec, called := serve([]string{"*"}, http.MethodGet, "https://anything.example", false)
		require.True(t, called)
		require.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
	})
}

func TestBodyLimit(t *testing.T) {
	var readErr error
	called := false
	h := middleware.BodyLimit(8)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, readErr = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("within limit", func(t *testing.T) {
		called, readErr = false, nil
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("12345678")))
		require.True(t, called)
		require.NoError(t, readErr)
	})

	t.Run("declared length over limit is rejected upfront", func(t *testing.T) {
		called, readErr = false, nil
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("123456789")))
		require.False(t, called)
		require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		require.JSONEq(t, `{"error":{"code":"body_too_large","message":"request body exceeds 8 bytes"}}`,
			rec.Body.String())
	})

	t.Run("streamed body over limit fails while reading", func(t *testing.T) {
		called, readErr = false, nil
		req := httptest.NewRequest(http.MethodPost, "/", io.NopCloser(strings.NewReader("123456789")))
		req.ContentLength = -1
		h.ServeHTTP(httptest.NewRecorder(), req)
		require.True(t, called)
		var maxBytesErr *http.MaxBytesError
		require.True(t, errors.As(readErr, &maxBytesErr), "expected MaxBytesError, got %v", readErr)
	})
}

func TestTimeout(t *testing.T) {
	var deadline time.Time
	var ok bool
	h := middleware.Timeout(2 * time.Second)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		deadline, ok = r.Context().Deadline()
	}))

	start := time.Now()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	require.True(t, ok)
	require.WithinDuration(t, start.Add(2*time.Second), deadline, time.Second)
}
