package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/AlexandrKhromov2005/vault-chat/internal/media/domain"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type memoryRepo struct {
	files map[string]*domain.File
	ready map[string]bool
	fail  bool
}

func (r *memoryRepo) Create(_ context.Context, f *domain.File) error { r.files[f.ID] = f; return nil }
func (r *memoryRepo) Ready(_ context.Context, id string) error {
	if r.fail {
		return errors.New("database down")
	}
	r.ready[id] = true
	return nil
}
func (r *memoryRepo) Get(_ context.Context, id string) (*domain.File, error) {
	if !r.ready[id] {
		return nil, domain.ErrNotFound
	}
	return r.files[id], nil
}
func (r *memoryRepo) RemovePending(_ context.Context, id string) error {
	if !r.ready[id] {
		delete(r.files, id)
	}
	return nil
}
func (r *memoryRepo) Expired(context.Context) ([]string, error) {
	var ids []string
	for id := range r.files {
		if !r.ready[id] {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

type memoryStore struct {
	blobs map[string]string
	fail  bool
	after func()
}

func (s *memoryStore) Put(_ context.Context, id string, data []byte, _ string) error {
	if s.fail {
		return errors.New("storage down")
	}
	s.blobs[id] = string(data)
	if s.after != nil {
		s.after()
	}
	return nil
}
func (s *memoryStore) Open(_ context.Context, id string) (io.ReadCloser, error) {
	v, ok := s.blobs[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return io.NopCloser(strings.NewReader(v)), nil
}
func (s *memoryStore) Remove(_ context.Context, id string) error { delete(s.blobs, id); return nil }

type roomGate struct{ denied bool }

func (g *roomGate) CheckRoom(context.Context, string) error {
	if g.denied {
		return domain.ErrNotFound
	}
	return nil
}
func fixture() (*Service, *memoryRepo, *memoryStore, *roomGate) {
	r := &memoryRepo{files: map[string]*domain.File{}, ready: map[string]bool{}}
	b := &memoryStore{blobs: map[string]string{}}
	g := &roomGate{}
	return New(r, b, g), r, b, g
}
func TestUploadAndAuthorizedDownload(t *testing.T) {
	s, r, _, g := fixture()
	ctx := context.Background()
	room, owner := uuid.NewString(), uuid.NewString()
	f, err := s.Upload(ctx, owner, room, "notes.txt", []byte("hello"))
	require.NoError(t, err)
	require.Equal(t, int64(5), f.Size)
	require.True(t, r.ready[f.ID])
	got, body, err := s.Download(ctx, f.ID)
	require.NoError(t, err)
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, "hello", string(data))
	require.Equal(t, f, got)
	g.denied = true
	_, _, err = s.Download(ctx, f.ID)
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = s.Get(ctx, uuid.NewString())
	require.ErrorIs(t, err, domain.ErrNotFound)
}
func TestRejectedAndFailedUploadsStayInvisible(t *testing.T) {
	for _, kind := range []string{"invalid", "denied", "storage", "removed", "commit"} {
		t.Run(kind, func(t *testing.T) {
			s, r, b, g := fixture()
			data := []byte("hello")
			switch kind {
			case "invalid":
				data = nil
			case "denied":
				g.denied = true
			case "storage":
				b.fail = true
			case "removed":
				b.after = func() { g.denied = true }
			case "commit":
				r.fail = true
			}
			_, err := s.Upload(context.Background(), uuid.NewString(), uuid.NewString(), "file.txt", data)
			require.Error(t, err)
			require.Empty(t, r.ready)
			if kind != "commit" {
				require.Empty(t, b.blobs)
				require.Empty(t, r.files)
			} else {
				require.Len(t, b.blobs, 1)
				require.NoError(t, s.Sweep(context.Background()))
				require.Empty(t, b.blobs)
				require.Empty(t, r.files)
			}
		})
	}
}
