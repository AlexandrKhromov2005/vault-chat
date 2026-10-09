// Package migrations embeds the SQL migration files of all services.
package migrations

import "embed"

// AuthFS contains the auth service migrations (auth/*.up.sql, auth/*.down.sql).
//
//go:embed auth
var AuthFS embed.FS

// ChatFS contains migrations for the independent Chat database.
//
//go:embed chat
var ChatFS embed.FS
