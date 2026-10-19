package handler

import (
	"bytes"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/response"
)

// webContentTypes lists the only file types the client consists of. Other
// files in the tree are not served, so nothing lands on the origin by being
// put next to the client.
var webContentTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".mjs":  "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".svg":  "image/svg+xml",
}

// webCSP allows only the client's own scripts, styles, images and API on the
// same origin (WebSocket included). Without 'unsafe-inline', markup injected
// through a rendering bug still could not run scripts. Forms are handled by
// scripts, so form submission and framing are disabled entirely.
const webCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; " +
	"connect-src 'self'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'"

// WebApp serves the browser client: index.html at "/" and its assets under
// "/app/". It never lists directories.
type WebApp struct {
	files fs.FS
}

// NewWebApp creates a handler serving the client from files.
func NewWebApp(files fs.FS) *WebApp {
	return &WebApp{files: files}
}

func (a *WebApp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name, ok := webFileName(r.URL.Path)
	contentType := webContentTypes[path.Ext(name)]
	if !ok || contentType == "" {
		notFoundError(w)
		return
	}

	data, err := fs.ReadFile(a.files, name)
	if err != nil {
		// Missing files and directories alike.
		notFoundError(w)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Security-Policy", webCSP)
	// The files are embedded in the binary; without a modification time
	// ServeContent sends no Last-Modified, and SecurityHeaders already
	// disables caching, so every load gets the running version.
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

// webFileName maps a request path to a file name in the client tree.
func webFileName(requestPath string) (string, bool) {
	if requestPath == "/" {
		return "index.html", true
	}
	rest, ok := strings.CutPrefix(requestPath, "/app/")
	if !ok || rest == "" {
		return "", false
	}
	name := "app/" + rest
	// Rejects "..", empty elements and trailing slashes.
	return name, fs.ValidPath(name)
}

func notFoundError(w http.ResponseWriter) {
	response.Error(w, http.StatusNotFound, "not_found", "route not found")
}
