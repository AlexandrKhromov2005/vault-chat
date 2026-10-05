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
