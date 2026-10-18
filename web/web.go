// Package web embeds the minimal browser client that the Gateway serves.
//
// The client is plain HTML, CSS and JavaScript modules without a build step
// or third-party code. Its logic is tested with node:test in web/test; this
// package only makes the files available to the Gateway binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var static embed.FS

// Files returns the client tree: index.html and the assets under app/.
func Files() fs.FS {
	files, err := fs.Sub(static, "static")
	if err != nil {
		// fs.Sub fails only for an invalid directory name, which is fixed above.
		panic(err)
	}
	return files
}
