// Package storage persists Media metadata and private S3 objects.
package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AlexandrKhromov2005/vault-chat/internal/media/domain"
)

// Postgres owns file metadata, independently of Auth and Chat databases.
type Postgres struct{ Pool *pgxpool.Pool }

// Create reserves an invisible pending upload before any object is stored.
func (p *Postgres) Create(ctx context.Context, f *domain.File) error {
	_, err := p.Pool.Exec(ctx, `INSERT INTO files(id,room_id,owner_id,name,content_type,size,created_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7)`, f.ID, f.RoomID, f.OwnerID, f.Name, f.ContentType, f.Size, f.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert pending file: %w", err)
	}
	return nil
}

// Ready atomically makes a completed upload visible.
func (p *Postgres) Ready(ctx context.Context, id string) error {
	tag, err := p.Pool.Exec(ctx, `UPDATE files SET ready=true WHERE id=$1::uuid AND NOT ready`, id)
	if err != nil {
		return fmt.Errorf("publish file: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrNotFound
	}
	return nil
}

// Get reads only completed uploads.
func (p *Postgres) Get(ctx context.Context, id string) (*domain.File, error) {
	f := &domain.File{}
	err := p.Pool.QueryRow(ctx, `SELECT id::text,room_id::text,owner_id::text,name,content_type,size,created_at FROM files WHERE id=$1::uuid AND ready`, id).Scan(&f.ID, &f.RoomID, &f.OwnerID, &f.Name, &f.ContentType, &f.Size, &f.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	return f, nil
}

// RemovePending cannot remove a published file, including after an uncertain commit.
func (p *Postgres) RemovePending(ctx context.Context, id string) error {
	_, err := p.Pool.Exec(ctx, `DELETE FROM files WHERE id=$1::uuid AND NOT ready`, id)
	if err != nil {
		return fmt.Errorf("delete pending file: %w", err)
	}
	return nil
}

// Expired selects abandoned uploads using the database clock, at most 100 per sweep.
func (p *Postgres) Expired(ctx context.Context) ([]string, error) {
	rows, err := p.Pool.Query(ctx, `SELECT id::text FROM files WHERE NOT ready AND created_at<now()-interval '1 hour' ORDER BY created_at LIMIT 100`)
	if err != nil {
		return nil, fmt.Errorf("query abandoned uploads: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("read abandoned uploads: %w", err)
	}
	return ids, nil
}
