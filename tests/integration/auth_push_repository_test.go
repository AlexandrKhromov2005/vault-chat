package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/migration"
)

// pushTokens returns the stored tokens of user by login.
func pushTokens(t *testing.T, pool *pgxpool.Pool, userID string) map[string]string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT family_id::text, token FROM push_devices WHERE user_id=$1::uuid`, userID)
	require.NoError(t, err)
	defer rows.Close()
	tokens := map[string]string{}
	for rows.Next() {
		var family, token string
		require.NoError(t, rows.Scan(&family, &token))
		tokens[family] = token
	}
	require.NoError(t, rows.Err())
	return tokens
}

func newLogin(t *testing.T, repo *repository.SessionRepository, userID string) *domain.Session {
	t.Helper()
	session := newTestSession(userID, time.Hour)
	require.NoError(t, repo.Create(context.Background(), session))
	return session
}

func TestPushTokens_RegisterReplaceAndMove(t *testing.T) {
	pool, repo, user := newSessionFixture(t)
	ctx := context.Background()
	phone, laptop := newLogin(t, repo, user.ID), newLogin(t, repo, user.ID)

	require.NoError(t, repo.RegisterPushToken(ctx, user.ID, phone.ID, "fcm", "token-a"))
	require.NoError(t, repo.RegisterPushToken(ctx, user.ID, phone.ID, "fcm", "token-a"), "registering again is idempotent")
	require.Equal(t, map[string]string{phone.ID: "token-a"}, pushTokens(t, pool, user.ID))

	require.NoError(t, repo.RegisterPushToken(ctx, user.ID, phone.ID, "fcm", "token-b"))
	require.Equal(t, map[string]string{phone.ID: "token-b"}, pushTokens(t, pool, user.ID), "a login keeps one token")

	// The access token of a rotated session still names its login.
	next := newTestSession(user.ID, time.Hour)
	require.NoError(t, repo.Rotate(ctx, phone.ID, next))
	require.NoError(t, repo.RegisterPushToken(ctx, user.ID, next.ID, "fcm", "token-c"))
	require.Equal(t, map[string]string{phone.ID: "token-c"}, pushTokens(t, pool, user.ID), "rotation keeps the login")

	other := newTestUser()
	require.NoError(t, repository.NewUserRepository(pool).Create(ctx, other))
	otherLogin := newLogin(t, repo, other.ID)
	require.NoError(t, repo.RegisterPushToken(ctx, other.ID, otherLogin.ID, "fcm", "token-c"))
	require.Empty(t, pushTokens(t, pool, user.ID), "a device token belongs to one login")
	require.Equal(t, map[string]string{otherLogin.ID: "token-c"}, pushTokens(t, pool, other.ID))

	require.NoError(t, repo.RegisterPushToken(ctx, user.ID, laptop.ID, "fcm", "token-d"))
	require.NoError(t, repo.UnregisterPushToken(ctx, user.ID, laptop.ID))
	require.NoError(t, repo.UnregisterPushToken(ctx, user.ID, laptop.ID), "unregistering is idempotent")
	require.NoError(t, repo.UnregisterPushToken(ctx, user.ID, otherLogin.ID), "another user's login is not ours")
	require.Empty(t, pushTokens(t, pool, user.ID))
	require.Len(t, pushTokens(t, pool, other.ID), 1)
}

func TestPushTokens_BoundedPerUser(t *testing.T) {
	pool, repo, user := newSessionFixture(t)
	ctx := context.Background()
	var logins []*domain.Session
	for i := range repository.MaxPushDevicesPerUser + 1 {
		login := newLogin(t, repo, user.ID)
		logins = append(logins, login)
		require.NoError(t, repo.RegisterPushToken(ctx, user.ID, login.ID, "fcm", fmt.Sprintf("token-%d", i)))
	}
	tokens := pushTokens(t, pool, user.ID)
	require.Len(t, tokens, repository.MaxPushDevicesPerUser)
	require.NotContains(t, tokens, logins[0].ID, "the least recently registered token is dropped")
}

// completeEmailAction consumes a password reset or email change of the
// verified user through the email repository, as the confirmation routes do.
func completeEmailAction(t *testing.T, pool *pgxpool.Pool, user *domain.User, purpose string) error {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE users SET email_verified_at=clock_timestamp() WHERE id=$1::uuid`, user.ID); err != nil {
		return err
	}
	now := time.Now()
	user.EmailVerifiedAt = &now
	repo := repository.NewEmailRepository(pool)
	action := emailAction(user, purpose)
	hash, notices := "", [][]byte{emailPayload(t, user.Email)}
	if purpose == "change" {
		action.TargetEmail = "changed-" + user.Email
		notices = append(notices, emailPayload(t, action.TargetEmail))
	} else {
		hash = "synthetic-new-password-hash"
	}
	if err := repo.QueueAction(ctx, action, emailPayload(t, action.TargetEmail)); err != nil {
		return err
	}
	_, err := repo.CompleteAction(ctx, action, hash, notices)
	return err
}

func TestPushTokens_RevocationDeletesTheLoginToken(t *testing.T) {
	revocations := map[string]func(*repository.SessionRepository, *pgxpool.Pool, *domain.User, *domain.Session) error{
		"logout": func(repo *repository.SessionRepository, _ *pgxpool.Pool, user *domain.User, s *domain.Session) error {
			return repo.RevokeFamily(context.Background(), user.ID, s.ID)
		},
		"revoke all or refresh reuse": func(repo *repository.SessionRepository, _ *pgxpool.Pool, user *domain.User, _ *domain.Session) error {
			_, err := repo.RevokeAllForUser(context.Background(), user.ID)
			return err
		},
		"password reset": func(_ *repository.SessionRepository, pool *pgxpool.Pool, user *domain.User, _ *domain.Session) error {
			return completeEmailAction(t, pool, user, "reset")
		},
		"email change": func(_ *repository.SessionRepository, pool *pgxpool.Pool, user *domain.User, _ *domain.Session) error {
			return completeEmailAction(t, pool, user, "change")
		},
	}
	for name, revoke := range revocations {
		t.Run(name, func(t *testing.T) {
			pool, repo, user := newSessionFixture(t)
			ctx := context.Background()
			login := newLogin(t, repo, user.ID)
			require.NoError(t, repo.RegisterPushToken(ctx, user.ID, login.ID, "fcm", "token-a"))
			// The token outlives rotation into the login's next session.
			next := newTestSession(user.ID, time.Hour)
			require.NoError(t, repo.Rotate(ctx, login.ID, next))
			require.Len(t, pushTokens(t, pool, user.ID), 1)

			require.NoError(t, revoke(repo, pool, user, next))
			require.Empty(t, pushTokens(t, pool, user.ID))
		})
	}
}

func TestPushTokens_BoundKeepsTheCallersToken(t *testing.T) {
	pool, repo, user := newSessionFixture(t)
	ctx := context.Background()
	for i := range repository.MaxPushDevicesPerUser {
		login := newLogin(t, repo, user.ID)
		require.NoError(t, repo.RegisterPushToken(ctx, user.ID, login.ID, "fcm", fmt.Sprintf("token-%d", i)))
	}
	// Registrations that waited for the user lock while others committed
	// must still rank newest: the caller's token is never the one dropped.
	_, err := pool.Exec(ctx, `UPDATE push_devices SET updated_at=clock_timestamp()+interval '1 hour' WHERE user_id=$1::uuid`, user.ID)
	require.NoError(t, err)
	login := newLogin(t, repo, user.ID)
	require.NoError(t, repo.RegisterPushToken(ctx, user.ID, login.ID, "fcm", "token-new"))
	tokens := pushTokens(t, pool, user.ID)
	require.Len(t, tokens, repository.MaxPushDevicesPerUser)
	require.Equal(t, "token-new", tokens[login.ID])
}

func TestPushTokens_RegistrationWaitsForConcurrentRevocation(t *testing.T) {
	pool, repo, user := newSessionFixture(t)
	ctx := context.Background()
	login := newLogin(t, repo, user.ID)

	// A revocation holds the per-user session lock and has revoked the login
	// but not committed yet.
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('auth.sessions:' || ($1::uuid)::text, 0))`, user.ID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE sessions SET revoked_at=clock_timestamp() WHERE family_id=$1::uuid AND revoked_at IS NULL`, login.ID)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- repo.RegisterPushToken(ctx, user.ID, login.ID, "fcm", "token-a") }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted)`).Scan(&waiting))
		if waiting {
			break
		}
		select {
		case err := <-done:
			require.NoError(t, tx.Commit(ctx))
			require.Failf(t, "registration did not wait for the revocation", "returned %v", err)
		default:
		}
		require.True(t, time.Now().Before(deadline), "registration neither waited nor returned")
		time.Sleep(10 * time.Millisecond)
	}
	require.NoError(t, tx.Commit(ctx))
	require.ErrorIs(t, <-done, repository.ErrSessionRevoked)
	require.Empty(t, pushTokens(t, pool, user.ID))
}

func TestPushTokens_RefusedForInactiveLogins(t *testing.T) {
	pool, repo, user := newSessionFixture(t)
	ctx := context.Background()

	revoked := newLogin(t, repo, user.ID)
	require.NoError(t, repo.RevokeFamily(ctx, user.ID, revoked.ID))
	require.ErrorIs(t, repo.RegisterPushToken(ctx, user.ID, revoked.ID, "fcm", "token-a"), repository.ErrSessionRevoked)

	expired := newTestSession(user.ID, -time.Minute)
	_, err := pool.Exec(ctx, `INSERT INTO sessions (id,user_id,family_id,expires_at) VALUES ($1::uuid,$2::uuid,$1::uuid,$3)`,
		expired.ID, user.ID, expired.ExpiresAt)
	require.NoError(t, err)
	require.ErrorIs(t, repo.RegisterPushToken(ctx, user.ID, expired.ID, "fcm", "token-a"), repository.ErrSessionRevoked)

	other := newTestUser()
	require.NoError(t, repository.NewUserRepository(pool).Create(ctx, other))
	login := newLogin(t, repo, user.ID)
	require.ErrorIs(t, repo.RegisterPushToken(ctx, other.ID, login.ID, "fcm", "token-a"), repository.ErrSessionRevoked,
		"a session of another user is not a login of ours")
	require.Empty(t, pushTokens(t, pool, user.ID))
	require.Empty(t, pushTokens(t, pool, other.ID))
}

func TestPushTokens_ExpiredLoginsArePrunedOnRegistration(t *testing.T) {
	pool, repo, user := newSessionFixture(t)
	ctx := context.Background()
	old := newLogin(t, repo, user.ID)
	require.NoError(t, repo.RegisterPushToken(ctx, user.ID, old.ID, "fcm", "token-old"))
	_, err := pool.Exec(ctx, `UPDATE sessions SET expires_at=clock_timestamp()-interval '1 minute' WHERE id=$1::uuid`, old.ID)
	require.NoError(t, err)

	// A login of an earlier credential version ended without a revocation.
	stale := newLogin(t, repo, user.ID)
	require.NoError(t, repo.RegisterPushToken(ctx, user.ID, stale.ID, "fcm", "token-stale"))
	_, err = pool.Exec(ctx, `UPDATE sessions SET credential_version=credential_version+1 WHERE family_id=$1::uuid`, stale.ID)
	require.NoError(t, err)

	login := newLogin(t, repo, user.ID)
	require.NoError(t, repo.RegisterPushToken(ctx, user.ID, login.ID, "fcm", "token-new"))
	require.Equal(t, map[string]string{login.ID: "token-new"}, pushTokens(t, pool, user.ID))
}

func TestPushTokens_ConcurrentRegistrationAndLogout(t *testing.T) {
	pool, repo, user := newSessionFixture(t)
	ctx := context.Background()
	for i := range 20 {
		login := newLogin(t, repo, user.ID)
		var wg sync.WaitGroup
		var registerErr, revokeErr error
		wg.Go(func() {
			registerErr = repo.RegisterPushToken(ctx, user.ID, login.ID, "fcm", fmt.Sprintf("token-%d", i))
		})
		wg.Go(func() { revokeErr = repo.RevokeFamily(ctx, user.ID, login.ID) })
		wg.Wait()
		require.NoError(t, revokeErr)
		if registerErr != nil {
			require.ErrorIs(t, registerErr, repository.ErrSessionRevoked)
		}
		require.Empty(t, pushTokens(t, pool, user.ID), "round %d left a token for a revoked login", i)
	}
}

// TestPushTokens_RuntimeRoleRunsTheRevocationTrigger runs as the Auth
// runtime role, which has DML but no DDL: the trigger must work with it.
func TestPushTokens_RuntimeRoleRunsTheRevocationTrigger(t *testing.T) {
	pool, _, user := newSessionFixture(t)
	ctx := context.Background()
	require.NoError(t, migration.ProvisionRuntime(ctx, pool, "auth_app", "test-runtime-password"))
	cfg := pool.Config()
	cfg.ConnConfig.User = "auth_app"
	cfg.ConnConfig.Password = "test-runtime-password"
	runtime, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer runtime.Close()
	repo := repository.NewSessionRepository(runtime)

	login := newLogin(t, repo, user.ID)
	require.NoError(t, repo.RegisterPushToken(ctx, user.ID, login.ID, "fcm", "token-a"))
	require.NoError(t, repo.RevokeFamily(ctx, user.ID, login.ID))
	require.Empty(t, pushTokens(t, pool, user.ID))
}
