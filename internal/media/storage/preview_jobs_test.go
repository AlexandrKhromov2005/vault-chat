package storage

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AlexandrKhromov2005/vault-chat/internal/media/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/migration"
	"github.com/AlexandrKhromov2005/vault-chat/migrations"
)

func previewTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("VAULT_CHAT_TEST_MEDIA_DATABASE_URL")
	if url == "" {
		t.Skip("dedicated Media database required")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migration.Apply(context.Background(), pool, migrations.MediaFS, "media"); err != nil {
		t.Fatal(err)
	}
	return pool
}

func insertPreviewFile(t *testing.T, pool *pgxpool.Pool, repo *Postgres, contentType string) *domain.File {
	t.Helper()
	file := &domain.File{ID: uuid.NewString(), RoomID: uuid.NewString(), OwnerID: uuid.NewString(), Name: "image", ContentType: contentType, Size: 1, CreatedAt: time.Now().UTC()}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM files WHERE id=$1::uuid", file.ID) })
	if err := repo.Create(context.Background(), file); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestReadyAtomicallyEnqueuesOnlyImages(t *testing.T) {
	pool := previewTestPool(t)
	repo := &Postgres{Pool: pool}
	image := insertPreviewFile(t, pool, repo, "image/png")
	text := insertPreviewFile(t, pool, repo, "text/plain")
	if err := repo.Ready(context.Background(), image.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.Ready(context.Background(), text.ID); err != nil {
		t.Fatal(err)
	}
	var imageJobs, textJobs int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM preview_jobs WHERE file_id=$1::uuid", image.ID).Scan(&imageJobs); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM preview_jobs WHERE file_id=$1::uuid", text.ID).Scan(&textJobs); err != nil {
		t.Fatal(err)
	}
	if imageJobs != 1 || textJobs != 0 {
		t.Fatalf("image jobs=%d, text jobs=%d", imageJobs, textJobs)
	}
}
