package storage

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/media/preview"
	"github.com/AlexandrKhromov2005/vault-chat/internal/media/service"
)

type pipelineDecoder struct {
	result preview.Result
	err    error
}

func (d pipelineDecoder) Decode(context.Context, []byte) (preview.Result, error) {
	return d.result, d.err
}

func TestPreviewPipelineStoresAndKeepsVisibleVersion(t *testing.T) {
	endpoint := os.Getenv("VAULT_CHAT_TEST_MEDIA_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("S3 endpoint required")
	}
	pool := previewTestPool(t)
	repo := &Postgres{Pool: pool}
	ctx := context.Background()
	blobs, err := NewS3(ctx, endpoint, "minioadmin", "minioadmin", "preview-test-"+uuid.NewString(), false)
	require.NoError(t, err)
	t.Cleanup(func() {
		for object := range blobs.client.ListObjects(ctx, blobs.bucket, minio.ListObjectsOptions{Recursive: true}) {
			_ = blobs.client.RemoveObject(ctx, blobs.bucket, object.Key, minio.RemoveObjectOptions{})
		}
		_ = blobs.client.RemoveBucket(ctx, blobs.bucket)
	})
	var original bytes.Buffer
	require.NoError(t, png.Encode(&original, image.NewNRGBA(image.Rect(0, 0, 2, 1))))
	file := insertPreviewFile(t, pool, repo, "image/png")
	require.NoError(t, blobs.Put(ctx, file.ID, original.Bytes(), file.ContentType))
	require.NoError(t, repo.Ready(ctx, file.ID))
	decoder := pipelineDecoder{result: preview.Result{Width: 2, Height: 1, RGB: make([]byte, 6)}}
	scheduler := service.NewPreviewScheduler(repo, blobs, []preview.Decoder{decoder}, "pipeline")
	require.NoError(t, scheduler.RunOnce(ctx))
	key, err := repo.GetPreview(ctx, file.ID)
	require.NoError(t, err)
	body, err := blobs.Open(ctx, key)
	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	require.NoError(t, body.Close())
	_, err = jpeg.Decode(bytes.NewReader(data))
	require.NoError(t, err)

	_, err = pool.Exec(ctx, "INSERT INTO preview_jobs(file_id,preset,decoder_version) VALUES($1::uuid,'p320','zig-2')", file.ID)
	require.NoError(t, err)
	failed := service.NewPreviewScheduler(repo, blobs, []preview.Decoder{pipelineDecoder{err: preview.ErrWorker}}, "pipeline-v2")
	require.NoError(t, failed.RunOnce(ctx))
	current, err := repo.GetPreview(ctx, file.ID)
	require.NoError(t, err)
	require.Equal(t, key, current)
}
