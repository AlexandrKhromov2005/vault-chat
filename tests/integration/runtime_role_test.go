package integration

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/migration"
)

func TestRuntimeRoleHasDMLButCannotChangeSchema(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	require.NoError(t, migration.ProvisionRuntime(ctx, pool, "auth_app", "test-runtime-password"))
	cfg := pool.Config()
	cfg.ConnConfig.User = "auth_app"
	cfg.ConnConfig.Password = "test-runtime-password"
	runtime, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer runtime.Close()
	_, err = runtime.Exec(ctx, "SELECT * FROM users LIMIT 1")
	require.NoError(t, err)
	for _, query := range []string{"CREATE TABLE forbidden (id int)", "DROP TABLE users", "ALTER TABLE users ADD COLUMN forbidden int", "CREATE SCHEMA forbidden"} {
		_, err = runtime.Exec(ctx, query)
		require.Error(t, err, query)
	}
	var superuser, createdb, createrole, bypass bool
	err = runtime.QueryRow(ctx, "SELECT rolsuper,rolcreatedb,rolcreaterole,rolbypassrls FROM pg_roles WHERE rolname=current_user").Scan(&superuser, &createdb, &createrole, &bypass)
	require.NoError(t, err)
	require.False(t, superuser || createdb || createrole || bypass)
}
