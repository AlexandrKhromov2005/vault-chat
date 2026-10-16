package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"io"
)

// S3 stores immutable objects; no public policy or presigned URLs are used.
type S3 struct {
	client *minio.Client
	bucket string
}

// NewS3 creates the dedicated bucket when absent and rejects bucket policies.
func NewS3(ctx context.Context, endpoint, access, secret, bucket string, secure bool) (*S3, error) {
	c, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: secure})
	if err != nil {
		return nil, fmt.Errorf("create S3 client: %w", err)
	}
	exists, err := c.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("check bucket: %w", err)
	}
	if !exists {
		if err := c.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			code := minio.ToErrorResponse(err).Code
			if code != "BucketAlreadyOwnedByYou" {
				return nil, fmt.Errorf("create private bucket: %w", err)
			}
		}
	}
	policy, err := c.GetBucketPolicy(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("check bucket policy: %w", err)
	}
	if policy != "" {
		return nil, errors.New("media bucket must not have a bucket policy")
	}
	return &S3{client: c, bucket: bucket}, nil
}

// Put uses a single bounded PUT so failed uploads leave no multipart sessions.
func (s *S3) Put(ctx context.Context, id string, data []byte, typ string) error {
	_, err := s.client.PutObject(ctx, s.bucket, id, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{ContentType: typ, DisableMultipart: true})
	if err != nil {
		return fmt.Errorf("put private object: %w", err)
	}
	return nil
}

// Open verifies existence before any HTTP download headers can be sent.
func (s *S3) Open(ctx context.Context, id string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, id, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get private object: %w", err)
	}
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		return nil, fmt.Errorf("stat private object: %w", err)
	}
	return obj, nil
}

// Remove is idempotent, including for failed single PUTs.
func (s *S3) Remove(ctx context.Context, id string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, id, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("remove private object: %w", err)
	}
	return nil
}

// Ping checks the bucket for service readiness.
func (s *S3) Ping(ctx context.Context) error {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("media bucket absent")
	}
	return nil
}
