package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AlexandrKhromov2005/vault-chat/internal/media/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/migration"
	"github.com/AlexandrKhromov2005/vault-chat/migrations"
)

func TestMetadataVisibility(t *testing.T) {
	url := os.Getenv("VAULT_CHAT_TEST_MEDIA_DATABASE_URL")
	if url == "" {
		t.Skip("dedicated Media database required")
	}
	ctx := context.Background()
	p, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := migration.Apply(ctx, p, migrations.MediaFS, "media"); err != nil {
		t.Fatal(err)
	}
	repo := &Postgres{Pool: p}
	f := &domain.File{ID: uuid.NewString(), RoomID: uuid.NewString(), OwnerID: uuid.NewString(), Name: "test.txt", ContentType: "text/plain", Size: 5, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	t.Cleanup(func() { _, _ = p.Exec(ctx, "DELETE FROM files WHERE id=$1::uuid", f.ID) })
	if err := repo.Create(ctx, f); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, f.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("pending visible: %v", err)
	}
	if _, err := p.Exec(ctx, "UPDATE files SET created_at=now()-interval '2 hours' WHERE id=$1::uuid", f.ID); err != nil {
		t.Fatal(err)
	}
	ids, err := repo.Expired(ctx)
	if err != nil || len(ids) != 1 || ids[0] != f.ID {
		t.Fatalf("expired=%v err=%v", ids, err)
	}
	if err := repo.Ready(ctx, f.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.RemovePending(ctx, f.ID); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, f.ID)
	if err != nil || got.Name != f.Name {
		t.Fatalf("ready=%v err=%v", got, err)
	}
	ids, err = repo.Expired(ctx)
	if err != nil || len(ids) != 0 {
		t.Fatalf("ready selected for cleanup: %v %v", ids, err)
	}
}

func TestPrivateS3(t *testing.T) {
	endpoint := os.Getenv("VAULT_CHAT_TEST_MEDIA_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("S3 endpoint required")
	}
	ctx := context.Background()
	bucket := "media-test-" + uuid.NewString()
	s, err := NewS3(ctx, endpoint, "minioadmin", "minioadmin", bucket, false)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	t.Cleanup(func() { _ = s.Remove(ctx, id); _ = s.client.RemoveBucket(ctx, bucket) })
	if err := s.Put(ctx, id, []byte("hello"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	r, err := s.Open(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil || string(data) != "hello" {
		t.Fatalf("body=%q %v", data, err)
	}
	response, err := http.Get("http://" + endpoint + "/" + bucket + "/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("anonymous download status=%d", response.StatusCode)
	}
	if err := s.Ping(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestS3RejectsPublicPolicy(t *testing.T) {
	endpoint := os.Getenv("VAULT_CHAT_TEST_MEDIA_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("S3 endpoint required")
	}
	ctx := context.Background()
	bucket := "media-test-" + uuid.NewString()
	s, err := NewS3(ctx, endpoint, "minioadmin", "minioadmin", bucket, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.client.SetBucketPolicy(ctx, bucket, ""); _ = s.client.RemoveBucket(ctx, bucket) })
	policy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::` + bucket + `/*"]}]}`
	if err := s.client.SetBucketPolicy(ctx, bucket, policy); err != nil {
		t.Fatal(err)
	}
	if _, err := NewS3(ctx, endpoint, "minioadmin", "minioadmin", bucket, false); err == nil {
		t.Fatal("public policy accepted")
	}
}
