// Package migrations embeds the SQL migration files of all services.
package migrations

import "embed"

// AuthFS contains the auth service migrations (auth/*.up.sql, auth/*.down.sql).
//
//go:embed auth
var AuthFS embed.FS
