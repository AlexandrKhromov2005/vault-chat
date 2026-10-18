package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
)

func newWebApp() *handler.WebApp {
	return handler.NewWebApp(fstest.MapFS{
		"index.html":       {Data: []byte("<!doctype html><title>Vault Chat</title>")},
		"app/app.mjs":      {Data: []byte("export {};")},
		"app/style.css":    {Data: []byte("body{}")},
		"app/icon.svg":     {Data: []byte("<svg/>")},
		"app/notes.txt":    {Data: []byte("not a client asset")},
		"app/lib/util.mjs": {Data: []byte("export {};")},
	})
}

func serveWeb(app *handler.WebApp, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestWebApp_ServesClient(t *testing.T) {
	tests := []struct {
		target      string
		contentType string
		body        string
	}{
		{"/", "text/html; charset=utf-8", "<!doctype html><title>Vault Chat</title>"},
		{"/app/app.mjs", "text/javascript; charset=utf-8", "export {};"},
		{"/app/lib/util.mjs", "text/javascript; charset=utf-8", "export {};"},
		{"/app/style.css", "text/css; charset=utf-8", "body{}"},
		{"/app/icon.svg", "image/svg+xml", "<svg/>"},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			rec := serveWeb(newWebApp(), http.MethodGet, tt.target)

			require.Equal(t, http.StatusOK, rec.Code)
			require.Equal(t, tt.contentType, rec.Header().Get("Content-Type"))
			require.Equal(t, tt.body, rec.Body.String())
		})
	}
}

func TestWebApp_ContentSecurityPolicy(t *testing.T) {
	rec := serveWeb(newWebApp(), http.MethodGet, "/")

	// Only the client's own scripts, styles and API; no inline code, no
	// framing, no plugins. Untrusted message text therefore cannot run even
	// if it ever reached the DOM as markup.
	require.Equal(t,
		"default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; "+
			"connect-src 'self'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'",
		rec.Header().Get("Content-Security-Policy"))
}

func TestWebApp_HeadRequest(t *testing.T) {
	rec := serveWeb(newWebApp(), http.MethodHead, "/")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, rec.Body.String())
}

func TestWebApp_RejectsEverythingElse(t *testing.T) {
	for name, target := range map[string]string{
		"missing file":           "/app/missing.mjs",
		"directory":              "/app/lib",
		"directory with slash":   "/app/lib/",
		"app root":               "/app/",
		"type outside allowlist": "/app/notes.txt",
		"index outside root":     "/app/index.html",
		"path traversal":         "/app/../index.html",
		"encoded traversal":      "/app/%2e%2e/index.html",
		"other path":             "/index.html",
	} {
		t.Run(name, func(t *testing.T) {
			rec := serveWeb(newWebApp(), http.MethodGet, target)

			require.Equal(t, http.StatusNotFound, rec.Code)
			require.JSONEq(t, `{"error":{"code":"not_found","message":"route not found"}}`, rec.Body.String())
		})
	}
}
