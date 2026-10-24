package migration

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProvisionRuntime grants data access without DDL or role/database ownership.
// Only the one-shot migration job receives the privileged connection/password.
func ProvisionRuntime(ctx context.Context, pool *pgxpool.Pool, role, password string) error {
	if role != "auth_app" && role != "chat_app" && role != "media_app" {
		return fmt.Errorf("unknown runtime role")
	}
	if password == "" {
		return fmt.Errorf("runtime password is required")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var exists bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname=$1)", role).Scan(&exists); err != nil {
		return err
	}
	name := pgx.Identifier{role}.Sanitize()
	if !exists {
		if _, err := tx.Exec(ctx, "CREATE ROLE "+name); err != nil {
			return err
		}
	}
	var statement string
	if err := tx.QueryRow(ctx, "SELECT format('ALTER ROLE %I WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD %L',$1::text,$2::text)", role, password).Scan(&statement); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, statement); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT parent.rolname FROM pg_auth_members m JOIN pg_roles parent ON parent.oid=m.roleid JOIN pg_roles child ON child.oid=m.member WHERE child.rolname=$1`, role)
	if err != nil {
		return err
	}
	parents, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, parent := range parents {
		if _, err := tx.Exec(ctx, "REVOKE "+pgx.Identifier{parent}.Sanitize()+" FROM "+name); err != nil {
			return err
		}
	}
	database := pgx.Identifier{pool.Config().ConnConfig.Database}.Sanitize()
	for _, query := range []string{
		"REVOKE ALL ON DATABASE " + database + " FROM PUBLIC",
		"GRANT CONNECT ON DATABASE " + database + " TO " + name,
		"REVOKE CREATE ON SCHEMA public FROM PUBLIC",
		"GRANT USAGE ON SCHEMA public TO " + name,
		"GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO " + name,
		"GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO " + name,
		"REVOKE ALL ON TABLE schema_migrations FROM " + name,
	} {
		if _, err := tx.Exec(ctx, query); err != nil {
			return fmt.Errorf("grant runtime permissions: %w", err)
		}
	}
	return tx.Commit(ctx)
}
