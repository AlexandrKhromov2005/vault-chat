package service

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
)

type missingUsers struct{ UserRepository }

func (missingUsers) GetByEmail(context.Context, string) (*domain.User, error) {
	return nil, repository.ErrNotFound
}

type blockingHasher struct {
	entered chan struct{}
	release chan struct{}
}

func (h *blockingHasher) Hash(password string) (string, error) {
	if password == "timing-equalization-dummy" {
		return "dummy", nil
	}
	h.entered <- struct{}{}
	<-h.release
	return "", errors.New("test hash failure")
}

func (h *blockingHasher) Verify(string, string) (bool, error) {
	h.entered <- struct{}{}
	<-h.release
	return false, nil
}

func TestPasswordWorkIsBoundedAcrossLoginAndRegistration(t *testing.T) {
	hasher := &blockingHasher{entered: make(chan struct{}, 3), release: make(chan struct{})}
	svc, err := NewService(missingUsers{}, nil, hasher, nil, slog.Default())
	require.NoError(t, err)
	done := make(chan error, 2)
	t.Cleanup(func() { close(hasher.release) })
	go func() {
		_, err := svc.Login(context.Background(), "missing@example.com", "password1")
		done <- err
	}()
	go func() {
		_, err := svc.Register(context.Background(), "new@example.com", "new_user", "password1")
		done <- err
	}()
	for range 2 {
		select {
		case <-hasher.entered:
		case <-time.After(time.Second):
			t.Fatal("password operation did not start")
		}
	}
	for _, register := range []bool{false, true} {
		result := make(chan error, 1)
		go func() {
			var err error
			if register {
				_, err = svc.Register(context.Background(), "other@example.com", "other_user", "password1")
			} else {
				_, err = svc.Login(context.Background(), "other@example.com", "password1")
			}
			result <- err
		}()
		select {
		case err := <-result:
			require.ErrorIs(t, err, ErrPasswordCapacity)
		case <-time.After(time.Second):
			t.Fatal("overload must be rejected without waiting for a hash")
		}
	}
	// Both failure paths must release their slots.
	hasher.release <- struct{}{}
	hasher.release <- struct{}{}
	<-done
	<-done
	go func() {
		_, err := svc.Login(context.Background(), "missing@example.com", "password1")
		done <- err
	}()
	select {
	case <-hasher.entered:
	case <-time.After(time.Second):
		t.Fatal("failed operations did not release password capacity")
	}
	hasher.release <- struct{}{}
	require.ErrorIs(t, <-done, ErrInvalidCredentials)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = svc.Register(ctx, "new@example.com", "new_user", "password1")
	require.ErrorIs(t, err, context.Canceled)
	select {
	case <-hasher.entered:
		t.Fatal("canceled request started password work")
	default:
	}
}
