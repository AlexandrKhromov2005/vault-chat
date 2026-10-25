package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
)

func TestStalePasswordWorkCannotCreateSessionAfterVersionChange(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	user := newTestUser()
	require.NoError(t, repository.NewUserRepository(pool).Create(ctx, user))
	// A login verified the old password before the security change committed.
	_, err := pool.Exec(ctx, "UPDATE users SET credential_version=1 WHERE id=$1::uuid", user.ID)
	require.NoError(t, err)
	session := &domain.Session{ID: uuid.NewString(), UserID: user.ID, ExpiresAt: time.Now().Add(time.Hour), CredentialVersion: 0}
	err = repository.NewSessionRepository(pool).Create(ctx, session)
	require.Error(t, err, "stale password work must not issue a session after credential version advances")
	var count int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE user_id=$1::uuid", user.ID).Scan(&count))
	require.Zero(t, count)
}

func TestEmailVerificationPolicyCoversExistingAndNewSessions(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	u := newTestUser()
	require.NoError(t, repository.NewUserRepository(pool).Create(ctx, u))
	s := &domain.Session{ID: uuid.NewString(), UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)}
	legacy := repository.NewSessionRepository(pool)
	require.NoError(t, legacy.Create(ctx, s))
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	blocked := repository.NewSessionRepository(pool, &past)
	allowed := repository.NewSessionRepository(pool, &future)
	active, err := blocked.IsActive(ctx, u.ID, s.ID)
	require.NoError(t, err)
	require.False(t, active)
	active, err = allowed.IsActive(ctx, u.ID, s.ID)
	require.NoError(t, err)
	require.True(t, active)
	next := &domain.Session{ID: uuid.NewString(), UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)}
	require.ErrorIs(t, blocked.Create(ctx, next), repository.ErrSessionRevoked)
	require.ErrorIs(t, blocked.Rotate(ctx, s.ID, next), repository.ErrSessionRevoked)
	_, err = pool.Exec(ctx, "UPDATE users SET email_verification_required=true WHERE id=$1::uuid", u.ID)
	require.NoError(t, err)
	// Stored strict policy cannot be bypassed by disabling mail delivery/config.
	active, err = legacy.IsActive(ctx, u.ID, s.ID)
	require.NoError(t, err)
	require.False(t, active)
	require.ErrorIs(t, legacy.Create(ctx, next), repository.ErrSessionRevoked)
	_, err = pool.Exec(ctx, "UPDATE users SET email_verified_at=clock_timestamp() WHERE id=$1::uuid", u.ID)
	require.NoError(t, err)
	active, err = blocked.IsActive(ctx, u.ID, s.ID)
	require.NoError(t, err)
	require.True(t, active)
	require.NoError(t, blocked.Create(ctx, next))
}
