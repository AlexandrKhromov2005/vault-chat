// Command migrate applies one service's schema and provisions its runtime role.
// The application containers never receive this job's owner credentials.
package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/migration"
	"github.com/AlexandrKhromov2005/vault-chat/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	service := os.Getenv("MIGRATION_SERVICE")
	var files fs.FS
	switch service {
	case "auth":
		files = migrations.AuthFS
	case "chat":
		files = migrations.ChatFS
	case "media":
		files = migrations.MediaFS
	default:
		return fmt.Errorf("MIGRATION_SERVICE must be auth, chat or media")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, os.Getenv("MIGRATION_DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()
	lock, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer lock.Release()
	if _, err := lock.Exec(ctx, "SELECT pg_advisory_lock(hashtext('vault:migrations'))"); err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = lock.Exec(cleanup, "SELECT pg_advisory_unlock(hashtext('vault:migrations'))")
	}()
	if err := migration.Apply(ctx, pool, files, service); err != nil {
		return err
	}
	return migration.ProvisionRuntime(ctx, pool, service+"_app", os.Getenv("MIGRATION_RUNTIME_PASSWORD"))
}
