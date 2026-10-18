package web_test

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/web"
)

var (
	// assetRef matches src/href attributes pointing at the client's own files.
	assetRef = regexp.MustCompile(`(?:src|href)="(/app/[^"]+)"`)
	// moduleImport matches relative static imports and re-exports.
	moduleImport = regexp.MustCompile(`(?m)(?:^|\s)(?:import|export)\s[^'"]*?['"](\.{1,2}/[^'"]+)['"]`)
)

func readFile(t *testing.T, name string) string {
	t.Helper()
	data, err := fs.ReadFile(web.Files(), name)
	require.NoError(t, err, name)
	return string(data)
}

func TestFiles_IndexReferencesExistingAssets(t *testing.T) {
	index := readFile(t, "index.html")

	refs := assetRef.FindAllStringSubmatch(index, -1)
	require.NotEmpty(t, refs, "index.html must load the client")
	for _, ref := range refs {
		_, err := fs.Stat(web.Files(), strings.TrimPrefix(ref[1], "/"))
		require.NoError(t, err, "index.html references %s", ref[1])
	}
}

// The Gateway serves the client with a CSP without 'unsafe-inline', so the
// browser would silently drop any of these.
func TestFiles_IndexIsCompatibleWithTheContentSecurityPolicy(t *testing.T) {
	index := readFile(t, "index.html")

	for name, pattern := range map[string]*regexp.Regexp{
		"inline script":   regexp.MustCompile(`<script(?:\s[^>]*)?>[^<]*\S[^<]*</script>`),
		"style element":   regexp.MustCompile(`<style[\s>]`),
		"style attribute": regexp.MustCompile(`\sstyle=`),
		"event handler":   regexp.MustCompile(`\son[a-z]+=`),
	} {
		require.Falsef(t, pattern.MatchString(index), "index.html contains a %s", name)
	}
}

func TestFiles_ModuleImportsResolve(t *testing.T) {
	var modules []string
	require.NoError(t, fs.WalkDir(web.Files(), "app", func(name string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(name, ".mjs") {
			modules = append(modules, name)
		}
		return err
	}))
	require.NotEmpty(t, modules)

	for _, module := range modules {
		for _, imp := range moduleImport.FindAllStringSubmatch(readFile(t, module), -1) {
			target := path.Join(path.Dir(module), imp[1])
			_, err := fs.Stat(web.Files(), target)
			require.NoError(t, err, "%s imports %s", module, imp[1])
		}
	}
}
