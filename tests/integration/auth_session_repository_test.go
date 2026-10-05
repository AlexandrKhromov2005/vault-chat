package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
)

// newSessionFixture prepares a session repository and a stored user that
// sessions can reference.
func newSessionFixture(t *testing.T) (*repository.SessionRepository, *domain.User) {
	t.Helper()

	pool := newTestPool(t)
	user := newTestUser()
	require.NoError(t, repository.NewUserRepository(pool).Create(context.Background(), user))

	return repository.NewSessionRepository(pool), user
}

func newTestSession(userID string, ttl time.Duration) *domain.Session {
	return &domain.Session{
		ID:        uuid.NewString(),
		UserID:    userID,
		ExpiresAt: time.Now().Add(ttl),
	}
}

func TestSessionRepository_Rotate(t *testing.T) {
	repo, user := newSessionFixture(t)
	ctx := context.Background()

	current := newTestSession(user.ID, time.Hour)
	require.NoError(t, repo.Create(ctx, current))

	next := newTestSession(user.ID, time.Hour)
	require.NoError(t, repo.Rotate(ctx, current.ID, next))

	err := repo.Rotate(ctx, current.ID, newTestSession(user.ID, time.Hour))
	require.ErrorIs(t, err, repository.ErrSessionReused, "a rotated session must not be usable again")

	require.NoError(t, repo.Rotate(ctx, next.ID, newTestSession(user.ID, time.Hour)),
		"the replacement session must be active")
}

func TestSessionRepository_Rotate_ConcurrentRotationsHaveOneWinner(t *testing.T) {
	repo, user := newSessionFixture(t)
	ctx := context.Background()

	current := newTestSession(user.ID, time.Hour)
	require.NoError(t, repo.Create(ctx, current))

	const attempts = 8
	errs := make([]error, attempts)
	var wg sync.WaitGroup
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = repo.Rotate(ctx, current.ID, newTestSession(user.ID, time.Hour))
		}()
	}
	wg.Wait()

	var succeeded int
	for _, err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		require.ErrorIs(t, err, repository.ErrSessionReused)
	}
	require.Equal(t, 1, succeeded)
}

func TestSessionRepository_Rotate_UnknownOrExpired(t *testing.T) {
	repo, user := newSessionFixture(t)
	ctx := context.Background()

	err := repo.Rotate(ctx, uuid.NewString(), newTestSession(user.ID, time.Hour))
	require.ErrorIs(t, err, repository.ErrNotFound)

	expired := newTestSession(user.ID, -time.Minute)
	require.NoError(t, repo.Create(ctx, expired))
	err = repo.Rotate(ctx, expired.ID, newTestSession(user.ID, time.Hour))
	require.ErrorIs(t, err, repository.ErrNotFound)
}

func TestSessionRepository_Revoke(t *testing.T) {
	repo, user := newSessionFixture(t)
	ctx := context.Background()

	session := newTestSession(user.ID, time.Hour)
	require.NoError(t, repo.Create(ctx, session))

	require.NoError(t, repo.Revoke(ctx, session.ID))
	require.NoError(t, repo.Revoke(ctx, session.ID), "revoke must be idempotent")
	require.NoError(t, repo.Revoke(ctx, uuid.NewString()), "revoking an unknown session is a no-op")

	err := repo.Rotate(ctx, session.ID, newTestSession(user.ID, time.Hour))
	require.ErrorIs(t, err, repository.ErrSessionRevoked)
}

func TestSessionRepository_RevokeAllForUser(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	users := repository.NewUserRepository(pool)
	repo := repository.NewSessionRepository(pool)

	user, other := newTestUser(), newTestUser()
	require.NoError(t, users.Create(ctx, user))
	require.NoError(t, users.Create(ctx, other))

	first := newTestSession(user.ID, time.Hour)
	second := newTestSession(user.ID, time.Hour)
	alreadyRevoked := newTestSession(user.ID, time.Hour)
	foreign := newTestSession(other.ID, time.Hour)
	for _, s := range []*domain.Session{first, second, alreadyRevoked, foreign} {
		require.NoError(t, repo.Create(ctx, s))
	}
	require.NoError(t, repo.Revoke(ctx, alreadyRevoked.ID))

	revoked, err := repo.RevokeAllForUser(ctx, user.ID)
	require.NoError(t, err)
	require.EqualValues(t, 2, revoked)

	for _, s := range []*domain.Session{first, second} {
		err := repo.Rotate(ctx, s.ID, newTestSession(user.ID, time.Hour))
		require.ErrorIs(t, err, repository.ErrSessionRevoked)
	}
	require.NoError(t, repo.Rotate(ctx, foreign.ID, newTestSession(other.ID, time.Hour)),
		"sessions of other users must stay active")
}
