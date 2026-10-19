package web_test

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/web"
)

// assetRef matches src/href attributes pointing at the client's own files.
var assetRef = regexp.MustCompile(`(?:src|href)="(/app/[^"]+)"`)

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
