// Package architecture checks the service boundaries of ADR 0008 and
// ADR 0010 in the code itself: a service binary never links another
// service's storage, migrations, RPC handlers or business logic.
package architecture

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const module = "github.com/AlexandrKhromov2005/vault-chat"

// services are the Go services that own their data.
var services = []string{"auth", "chat", "media", "gateway"}

// commands maps every binary that belongs to a service to its owner.
// Operator tools without one (migrate, healthcheck, s3-init) are checked by
// the deployment policy instead.
var commands = map[string]string{
	"auth":    "auth",
	"invite":  "auth",
	"chat":    "chat",
	"media":   "media",
	"preview": "media",
	"gateway": "gateway",
}

// shareable lists the packages of a service that others may compile in:
// input validation and the plain types it returns, with no I/O. Gateway
// validates requests with them before it calls the owner over gRPC.
var shareable = []string{"validator", "domain"}

// dependencies returns every non-test package of the module under the
// patterns with its transitive dependencies.
func dependencies(t *testing.T, patterns ...string) map[string][]string {
	t.Helper()
	args := append([]string{"list", "-f", "{{.ImportPath}}{{range .Deps}} {{.}}{{end}}"}, patterns...)
	cmd := exec.Command("go", args...)
	cmd.Dir = "../.."
	out, err := cmd.Output()
	require.NoError(t, err, "go list")
	packages := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		packages[fields[0]] = fields[1:]
	}
	return packages
}

// owner returns the service a package of the module belongs to, if any.
func owner(pkg string) (string, bool) {
	rest, ok := strings.CutPrefix(pkg, module+"/")
	if !ok {
		return "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return "", false
	}
	switch parts[0] {
	case "internal":
		for _, s := range services {
			if parts[1] == s {
				return s, true
			}
		}
	case "cmd":
		s, ok := commands[parts[1]]
		return s, ok
	}
	return "", false
}

// violations lists the dependencies that break the boundary of pkg.
func violations(pkg string, deps []string) []string {
	service, ok := owner(pkg)
	if !ok {
		return nil
	}
	var found []string
	for _, dep := range deps {
		if dep == module+"/migrations" && pkg != module+"/cmd/migrate" {
			found = append(found, dep)
			continue
		}
		other, ok := owner(dep)
		if !ok || other == service || !strings.HasPrefix(dep, module+"/internal/") {
			continue
		}
		allowed := false
		for _, name := range shareable {
			if dep == module+"/internal/"+other+"/"+name {
				allowed = true
			}
		}
		if !allowed {
			found = append(found, dep)
		}
	}
	return found
}

func TestServicesLinkOnlyTheirOwnCodeAndData(t *testing.T) {
	packages := dependencies(t, "./internal/...", "./cmd/...")
	checked := 0
	for pkg, deps := range packages {
		if _, ok := owner(pkg); !ok {
			continue
		}
		checked++
		require.Empty(t, violations(pkg, deps), "%s crosses a service boundary", pkg)
	}
	// Guard against a pattern or module path that silently matches nothing.
	require.Greater(t, checked, 30)
	for name := range commands {
		require.Contains(t, packages, module+"/cmd/"+name)
	}
}

func TestViolationsReportCrossServiceStorageAndMigrations(t *testing.T) {
	gateway := module + "/internal/gateway/handler"
	require.Equal(t, []string{module + "/internal/chat/repository"},
		violations(gateway, []string{module + "/internal/chat/validator", module + "/internal/chat/repository"}))
	require.Equal(t, []string{module + "/internal/auth/service"},
		violations(module+"/cmd/media", []string{module + "/internal/media/service", module + "/internal/auth/service"}))
	require.Equal(t, []string{module + "/migrations"},
		violations(module+"/internal/chat/service", []string{module + "/migrations", module + "/internal/shared/migration"}))
	require.Empty(t, violations(module+"/cmd/migrate", []string{module + "/migrations"}))
	require.Empty(t, violations(module+"/internal/shared/jwt", []string{module + "/internal/auth/service"}))
}
