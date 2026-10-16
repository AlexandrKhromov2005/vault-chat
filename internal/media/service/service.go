// Package service manages room-scoped immutable files and private blob storage.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/AlexandrKhromov2005/vault-chat/internal/media/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/media/validator"
)

// Repository owns Media metadata. Pending rows are never visible to readers.
type Repository interface {
	Create(context.Context, *domain.File) error
	Ready(context.Context, string) error
	Get(context.Context, string) (*domain.File, error)
	RemovePending(context.Context, string) error
	Expired(context.Context) ([]string, error)
}

// Store owns private blobs; Remove must be idempotent.
type Store interface {
	Put(context.Context, string, []byte, string) error
	Open(context.Context, string) (io.ReadCloser, error)
	Remove(context.Context, string) error
}

// RoomGate checks the current actor through Chat using the request credential.
type RoomGate interface {
	CheckRoom(context.Context, string) error
}

// Service never exposes a file without a current membership check.
type Service struct {
	repo  Repository
	blobs Store
	gate  RoomGate
}

// New binds metadata, private object storage and room authorization.
func New(repo Repository, blobs Store, gate RoomGate) *Service {
	return &Service{repo: repo, blobs: blobs, gate: gate}
}

// CheckUpload rejects invalid metadata and nonmembers before reading a body.
func (s *Service) CheckUpload(ctx context.Context, room, name string) error {
	if id, err := uuid.Parse(room); err != nil || id == uuid.Nil {
		return validator.ErrInvalidFile
	}
	if err := validator.Filename(name); err != nil {
		return err
	}
	return s.gate.CheckRoom(ctx, room)
}

// Upload publishes metadata only after the complete blob is stored. A second
// membership check catches removal while the object was being uploaded.
func (s *Service) Upload(ctx context.Context, owner, room, name string, data []byte) (*domain.File, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if id, err := uuid.Parse(owner); err != nil || id == uuid.Nil {
		return nil, validator.ErrInvalidFile
	}
	if err := s.CheckUpload(ctx, room, name); err != nil {
		return nil, err
	}
	typ, err := validator.Content(data)
	if err != nil {
		return nil, err
	}
	f := &domain.File{ID: uuid.NewString(), RoomID: room, OwnerID: owner, Name: name, ContentType: typ, Size: int64(len(data)), CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if err := s.repo.Create(ctx, f); err != nil {
		return nil, fmt.Errorf("create pending file: %w", err)
	}
	if err := s.blobs.Put(ctx, f.ID, data, typ); err != nil {
		return nil, s.discard(ctx, f.ID, err)
	}
	if err := s.gate.CheckRoom(ctx, room); err != nil {
		return nil, s.discard(ctx, f.ID, err)
	}
	if err := s.repo.Ready(ctx, f.ID); err != nil {
		// Commit outcome can be unknown on a lost connection. Keep the object:
		// deleting it might break a committed ready row. Sweep reclaims pending rows.
		return nil, fmt.Errorf("publish file metadata: %w", err)
	}
	return f, nil
}
func (s *Service) discard(ctx context.Context, id string, cause error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.blobs.Remove(ctx, id); err != nil {
		return errors.Join(cause, fmt.Errorf("remove pending blob: %w", err))
	}
	return errors.Join(cause, s.repo.RemovePending(ctx, id))
}

// Get returns metadata only to a current room member.
func (s *Service) Get(ctx context.Context, id string) (*domain.File, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil {
		return nil, validator.ErrInvalidFile
	}
	f, err := s.repo.Get(ctx, parsed.String())
	if err != nil {
		return nil, err
	}
	if err := s.gate.CheckRoom(ctx, f.RoomID); err != nil {
		return nil, err
	}
	return f, nil
}

// Download checks membership before opening storage; no presigned URLs escape.
func (s *Service) Download(ctx context.Context, id string) (*domain.File, io.ReadCloser, error) {
	f, err := s.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	body, err := s.blobs.Open(ctx, f.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("open private blob: %w", err)
	}
	return f, body, nil
}

// Sweep reclaims only uploads still pending for over an hour, beyond the 30s
// upload deadline. Ready rows are never selected or removed.
func (s *Service) Sweep(ctx context.Context) error {
	ids, err := s.repo.Expired(ctx)
	if err != nil {
		return fmt.Errorf("list expired uploads: %w", err)
	}
	for _, id := range ids {
		if err := s.discard(ctx, id, nil); err != nil {
			return err
		}
	}
	return nil
}
