// Package integration contains tests that require running infrastructure
// (PostgreSQL, Redis, ...) from deployments/docker-compose.yml. They skip
// silently when the corresponding environment variable is not set.
package integration

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/migrations"
)

func newTestRepository(t *testing.T) *repository.UserRepository {
	t.Helper()

	return repository.NewUserRepository(newTestPool(t))
}

// newTestPool connects to the test database, applies the auth migrations and
// wipes all auth tables.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("VAULT_CHAT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("VAULT_CHAT_TEST_DATABASE_URL is not set; skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	require.NoError(t, repository.Migrate(ctx, pool, migrations.AuthFS, "auth"))

	_, err = pool.Exec(ctx, "TRUNCATE users, registration_invitations CASCADE")
	require.NoError(t, err)

	return pool
}

func newTestUser() *domain.User {
	return &domain.User{
		ID:           uuid.NewString(),
		Email:        "user-" + uuid.NewString() + "@example.com",
		Username:     "u" + uuid.NewString()[:8],
		PasswordHash: "$argon2id$v=19$m=1024,t=1,p=2$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5",
	}
}

func TestUserRepository_CreateAndGetByEmail(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	user := newTestUser()
	require.NoError(t, repo.Create(ctx, user))

	got, err := repo.GetByEmail(ctx, user.Email)
	require.NoError(t, err)
	require.Equal(t, user.ID, got.ID)
	require.Equal(t, user.Email, got.Email)
	require.Equal(t, user.Username, got.Username)
	require.Equal(t, user.PasswordHash, got.PasswordHash)
	require.False(t, got.CreatedAt.IsZero())
}

func TestUserRepository_GetByEmail_CaseInsensitive(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	user := newTestUser()
	user.Email = "Case.Sensitive@Example.COM"
	require.NoError(t, repo.Create(ctx, user))

	got, err := repo.GetByEmail(ctx, "case.sensitive@example.com")
	require.NoError(t, err)
	require.Equal(t, user.ID, got.ID)
}

func TestUserRepository_Create_Duplicates(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	user := newTestUser()
	require.NoError(t, repo.Create(ctx, user))

	sameEmail := newTestUser()
	sameEmail.Email = user.Email
	err := repo.Create(ctx, sameEmail)
	require.ErrorIs(t, err, repository.ErrEmailTaken)

	sameUsername := newTestUser()
	sameUsername.Username = user.Username
	err = repo.Create(ctx, sameUsername)
	require.ErrorIs(t, err, repository.ErrUsernameTaken)

	sameUsername.ID = uuid.NewString()
	sameUsername.Username = strings.ToUpper(user.Username)
	err = repo.Create(ctx, sameUsername)
	require.ErrorIs(t, err, repository.ErrUsernameTaken)
}

func TestUserRepository_GetByID(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	user := newTestUser()
	require.NoError(t, repo.Create(ctx, user))

	got, err := repo.GetByID(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, user.Email, got.Email)

	_, err = repo.GetByID(ctx, uuid.NewString())
	require.True(t, errors.Is(err, repository.ErrNotFound), "expected ErrNotFound, got %v", err)

	_, err = repo.GetByEmail(ctx, "nobody-"+uuid.NewString()+"@example.com")
	require.True(t, errors.Is(err, repository.ErrNotFound), "expected ErrNotFound, got %v", err)
}

func TestUserRepository_SearchByUsernamePrefix(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	ids := map[string]string{}
	for _, name := range []string{"bobby", "Bob", "bo_b", "boxb", "bob-x", "alice", "BOBCAT"} {
		user := newTestUser()
		user.Username = name
		require.NoError(t, repo.Create(ctx, user))
		ids[name] = user.ID
	}
	search := func(prefix, excludeID string, limit int) []string {
		t.Helper()
		users, err := repo.SearchByUsernamePrefix(ctx, prefix, excludeID, limit)
		require.NoError(t, err)
		names := []string{}
		for _, user := range users {
			require.Equal(t, ids[user.Username], user.ID)
			require.Empty(t, user.Email)
			require.Empty(t, user.PasswordHash)
			names = append(names, user.Username)
		}
		return names
	}

	// Case-insensitive, exact matches first, the excluded caller is skipped.
	require.Equal(t, []string{"Bob", "bob-x", "bobby"}, search("BOB", ids["BOBCAT"], 10))
	require.Equal(t, []string{"bo_b", "Bob"}, search("b", uuid.NewString(), 2))
	// LIKE metacharacters are literal.
	require.Equal(t, []string{"bo_b"}, search("bo_", uuid.NewString(), 10))
	for _, prefix := range []string{"%", `\`, "zz"} {
		require.Empty(t, search(prefix, uuid.NewString(), 10), prefix)
	}
}

// searchQueryRecorder captures the SQL and arguments of the user search so
// the test can EXPLAIN exactly what the repository sends.
type searchQueryRecorder struct {
	sql  string
	args []any
}

func (r *searchQueryRecorder) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "FROM users") && strings.Contains(data.SQL, "ORDER BY") {
		r.sql, r.args = data.SQL, data.Args
	}
	return ctx
}

func (*searchQueryRecorder) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestUserRepository_SearchByUsernamePrefix_UsesPrefixIndex(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	recorder := &searchQueryRecorder{}
	config := pool.Config()
	config.ConnConfig.Tracer = recorder
	traced, err := pgxpool.NewWithConfig(ctx, config)
	require.NoError(t, err)
	t.Cleanup(traced.Close)

	_, err = repository.NewUserRepository(traced).SearchByUsernamePrefix(ctx, "Bo_", uuid.NewString(), 10)
	require.NoError(t, err)
	require.NotEmpty(t, recorder.sql)

	// On a small table the planner prefers a sequential scan, so rule it out
	// to see whether the index can serve the search at all.
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	_, err = tx.Exec(ctx, "SET LOCAL enable_seqscan = off")
	require.NoError(t, err)
	rows, err := tx.Query(ctx, "EXPLAIN "+recorder.sql, recorder.args...)
	require.NoError(t, err)
	lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	plan := strings.Join(lines, "\n")

	require.Contains(t, plan, "users_username_lower_idx", plan)
	// The prefix is matched by the index range alone, without a LIKE recheck.
	require.NotContains(t, plan, "~~", plan)
}

func TestMigrate_Idempotent(t *testing.T) {
	dsn := os.Getenv("VAULT_CHAT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("VAULT_CHAT_TEST_DATABASE_URL is not set; skipping integration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	require.NoError(t, repository.Migrate(ctx, pool, migrations.AuthFS, "auth"))
	require.NoError(t, repository.Migrate(ctx, pool, migrations.AuthFS, "auth"))
}

func TestUsernameMigrationPreservesExistingCaseCollisions(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	// Recreate the old uniqueness rule inside a rollback-only transaction.
	_, err = tx.Exec(ctx, `DROP INDEX users_username_casefold_unique;
CREATE UNIQUE INDEX users_username_unique ON users (username)`)
	require.NoError(t, err)
	for _, name := range []string{"Admin", "admin"} {
		_, err = tx.Exec(ctx, `INSERT INTO users (id,email,username,password_hash) VALUES ($1,$2,$3,'hash')`, uuid.NewString(), name+"@"+uuid.NewString()+".example", name)
		require.NoError(t, err)
	}
	sql, err := migrations.AuthFS.ReadFile("auth/0004_username_casefold_unique.up.sql")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, "SAVEPOINT migration_attempt")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, string(sql))
	require.Error(t, err, "colliding account names must require operator resolution")
	_, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT migration_attempt")
	require.NoError(t, err)
	var names []string
	err = tx.QueryRow(ctx, "SELECT array_agg(username ORDER BY username) FROM users").Scan(&names)
	require.NoError(t, err)
	require.Equal(t, []string{"Admin", "admin"}, names)
}
