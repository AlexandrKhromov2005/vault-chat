// Package integration contains tests that require running infrastructure
// (PostgreSQL, Redis, ...) from deployments/docker-compose.yml. They skip
// silently when the corresponding environment variable is not set.
package integration

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
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

	_, err = pool.Exec(ctx, "TRUNCATE users CASCADE")
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
