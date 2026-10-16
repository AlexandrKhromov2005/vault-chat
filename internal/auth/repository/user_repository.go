// Package repository implements the PostgreSQL storage layer of the auth
// service using pgx.
package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
)

var (
	// ErrNotFound is returned when the requested row does not exist.
	ErrNotFound = errors.New("repository: not found")
	// ErrEmailTaken is returned on a unique violation of the email index.
	ErrEmailTaken = errors.New("repository: email already registered")
	// ErrUsernameTaken is returned on a unique violation of the username index.
	ErrUsernameTaken = errors.New("repository: username already taken")
)

// uniqueViolation is the PostgreSQL error code for unique constraint violations.
const uniqueViolation = "23505"

// UserRepository persists users in PostgreSQL.
type UserRepository struct {
	pool *pgxpool.Pool
}

// NewUserRepository creates a repository on top of the given connection pool.
func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

// Create inserts a new user. It returns ErrEmailTaken or ErrUsernameTaken on
// unique constraint violations.
func (r *UserRepository) Create(ctx context.Context, user *domain.User) error {
	const query = `INSERT INTO users (id, email, username, password_hash)
		VALUES ($1::uuid, $2, $3, $4)`

	_, err := r.pool.Exec(ctx, query, user.ID, user.Email, user.Username, user.PasswordHash)
	if err == nil {
		return nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		switch {
		case strings.Contains(pgErr.ConstraintName, "email"):
			return ErrEmailTaken
		case strings.Contains(pgErr.ConstraintName, "username"):
			return ErrUsernameTaken
		}
	}
	return fmt.Errorf("failed to insert user: %w", err)
}

// GetByEmail returns the user with the given email. Lookup is
// case-insensitive (the column is CITEXT). Returns ErrNotFound when absent.
func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	const query = `SELECT id::text, email, username, password_hash, created_at, updated_at
		FROM users WHERE email = $1`

	return r.scanUser(r.pool.QueryRow(ctx, query, email))
}

// GetByID returns the user with the given id. Returns ErrNotFound when absent.
func (r *UserRepository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	const query = `SELECT id::text, email, username, password_hash, created_at, updated_at
		FROM users WHERE id = $1::uuid`

	return r.scanUser(r.pool.QueryRow(ctx, query, id))
}

// errUnsupportedPrefix is returned for a search prefix without a computed
// byte-order upper bound. Every query accepted by the auth validator is
// supported: a non-empty ASCII string without DEL.
var errUnsupportedPrefix = errors.New("repository: unsupported username prefix")

// usernamePrefixRange returns the lowercased prefix and the smallest string
// that is greater, in byte order, than every string starting with it: the
// prefix with its last byte incremented. Bytes from 0x7f up are rejected, so
// the bound stays ASCII and valid UTF-8.
func usernamePrefixRange(prefix string) (lower, upper string, err error) {
	if prefix == "" {
		return "", "", errUnsupportedPrefix
	}
	for i := range len(prefix) {
		if prefix[i] >= 0x7f {
			return "", "", errUnsupportedPrefix
		}
	}
	lower = strings.ToLower(prefix)
	last := len(lower) - 1
	return lower, lower[:last] + string(lower[last]+1), nil
}

// SearchByUsernamePrefix returns at most limit users other than excludeID
// whose username starts with prefix, ignoring case. Only ID and Username are
// loaded. Users are ordered by lowercased username in byte order, which puts
// an exact match first, then by username.
func (r *UserRepository) SearchByUsernamePrefix(
	ctx context.Context,
	prefix, excludeID string,
	limit int,
) ([]*domain.User, error) {
	// The COLLATE "C" expression matches users_username_lower_idx, so the
	// byte-order range [$1, $2) is an index range scan with no recheck.
	const query = `SELECT id::text, username FROM users
		WHERE lower(username) COLLATE "C" >= $1 AND lower(username) COLLATE "C" < $2
			AND id <> $3::uuid
		ORDER BY lower(username) COLLATE "C", username COLLATE "C"
		LIMIT $4`

	lower, upper, err := usernamePrefixRange(prefix)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, query, lower, upper, excludeID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to search users: %w", err)
	}
	users, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*domain.User, error) {
		var user domain.User
		return &user, row.Scan(&user.ID, &user.Username)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scan users: %w", err)
	}
	return users, nil
}

func (r *UserRepository) scanUser(row pgx.Row) (*domain.User, error) {
	var user domain.User
	err := row.Scan(&user.ID, &user.Email, &user.Username,
		&user.PasswordHash, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan user: %w", err)
	}
	return &user, nil
}
