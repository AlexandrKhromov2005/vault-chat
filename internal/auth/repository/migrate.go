package repository

import (
	"context"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/migration"
)

// Migrate applies Auth migrations using the shared runner. Kept for existing
// callers; each service supplies its own embedded files and database pool.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, dir string) error {
	return migration.Apply(ctx, pool, fsys, dir)
}
