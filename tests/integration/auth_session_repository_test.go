package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
)

// newSessionFixture prepares a session repository and a stored user that
// sessions can reference.
func newSessionFixture(t *testing.T) (*pgxpool.Pool, *repository.SessionRepository, *domain.User) {
	t.Helper()

	pool := newTestPool(t)
	user := newTestUser()
	require.NoError(t, repository.NewUserRepository(pool).Create(context.Background(), user))

	return pool, repository.NewSessionRepository(pool), user
}

func newTestSession(userID string, ttl time.Duration) *domain.Session {
	return &domain.Session{
		ID:        uuid.NewString(),
		UserID:    userID,
		ExpiresAt: time.Now().Add(ttl),
	}
}

func TestSessionRepository_Rotate(t *testing.T) {
	_, repo, user := newSessionFixture(t)
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
	_, repo, user := newSessionFixture(t)
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
	_, repo, user := newSessionFixture(t)
	ctx := context.Background()

	err := repo.Rotate(ctx, uuid.NewString(), newTestSession(user.ID, time.Hour))
	require.ErrorIs(t, err, repository.ErrNotFound)

	expired := newTestSession(user.ID, -time.Minute)
	require.NoError(t, repo.Create(ctx, expired))
	err = repo.Rotate(ctx, expired.ID, newTestSession(user.ID, time.Hour))
	require.ErrorIs(t, err, repository.ErrNotFound)
}

func TestSessionRepository_Rotate_SessionOfAnotherUser(t *testing.T) {
	pool, repo, user := newSessionFixture(t)
	ctx := context.Background()
	other := newTestUser()
	require.NoError(t, repository.NewUserRepository(pool).Create(ctx, other))

	session := newTestSession(user.ID, time.Hour)
	require.NoError(t, repo.Create(ctx, session))

	err := repo.Rotate(ctx, session.ID, newTestSession(other.ID, time.Hour))
	require.ErrorIs(t, err, repository.ErrNotFound)

	require.NoError(t, repo.Rotate(ctx, session.ID, newTestSession(user.ID, time.Hour)),
		"a rejected rotation must leave the session active")
}

func TestSessionRepository_RevokeFamily(t *testing.T) {
	_, repo, user := newSessionFixture(t)
	ctx := context.Background()

	first := newTestSession(user.ID, time.Hour)
	require.NoError(t, repo.Create(ctx, first))
	second := newTestSession(user.ID, time.Hour)
	require.NoError(t, repo.Rotate(ctx, first.ID, second))
	otherDevice := newTestSession(user.ID, time.Hour)
	require.NoError(t, repo.Create(ctx, otherDevice))

	// Logging out with a token that was already rotated still ends the
	// session it was rotated into.
	require.NoError(t, repo.RevokeFamily(ctx, user.ID, first.ID))
	err := repo.Rotate(ctx, second.ID, newTestSession(user.ID, time.Hour))
	require.ErrorIs(t, err, repository.ErrSessionRevoked)

	require.NoError(t, repo.RevokeFamily(ctx, user.ID, first.ID), "revoke must be idempotent")
	require.NoError(t, repo.RevokeFamily(ctx, user.ID, uuid.NewString()), "revoking an unknown session is a no-op")
	require.NoError(t, repo.RevokeFamily(ctx, uuid.NewString(), otherDevice.ID),
		"a session cannot be revoked on behalf of another user")

	require.NoError(t, repo.Rotate(ctx, otherDevice.ID, newTestSession(user.ID, time.Hour)),
		"sessions of other logins must stay active")
}

func TestSessionRepository_RevokeFamily_DuringRotation(t *testing.T) {
	pool, repo, user := newSessionFixture(t)
	ctx := context.Background()

	current := newTestSession(user.ID, time.Hour)
	require.NoError(t, repo.Create(ctx, current))
	next := newTestSession(user.ID, time.Hour)
	stallInsertOf(t, pool, next.ID)

	rotated := make(chan error, 1)
	go func() { rotated <- repo.Rotate(ctx, current.ID, next) }()
	waitForStalledInsert(t, pool)

	require.NoError(t, repo.RevokeFamily(ctx, user.ID, current.ID))
	require.NoError(t, <-rotated)

	err := repo.Rotate(ctx, next.ID, newTestSession(user.ID, time.Hour))
	require.ErrorIs(t, err, repository.ErrSessionRevoked,
		"a session created by a concurrent rotation must not survive logout")
}

func TestSessionRepository_RevokeAllForUser_DuringRotation(t *testing.T) {
	pool, repo, user := newSessionFixture(t)
	ctx := context.Background()

	current := newTestSession(user.ID, time.Hour)
	require.NoError(t, repo.Create(ctx, current))
	next := newTestSession(user.ID, time.Hour)
	stallInsertOf(t, pool, next.ID)

	rotated := make(chan error, 1)
	go func() { rotated <- repo.Rotate(ctx, current.ID, next) }()
	waitForStalledInsert(t, pool)

	_, err := repo.RevokeAllForUser(ctx, user.ID)
	require.NoError(t, err)
	require.NoError(t, <-rotated)

	err = repo.Rotate(ctx, next.ID, newTestSession(user.ID, time.Hour))
	require.ErrorIs(t, err, repository.ErrSessionRevoked,
		"a session created by a concurrent rotation must not survive revoke-all")
}

func TestSessions_DeletedWithUser(t *testing.T) {
	pool, repo, user := newSessionFixture(t)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, newTestSession(user.ID, time.Hour)))
	_, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", user.ID)
	require.NoError(t, err)

	var left int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM sessions WHERE user_id = $1::uuid", user.ID).Scan(&left))
	require.Zero(t, left)
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
	require.NoError(t, repo.RevokeFamily(ctx, user.ID, alreadyRevoked.ID))

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

// stallInsertOf makes the insert of the session with the given id sleep, so
// that a test can act while the inserting transaction is still open.
func stallInsertOf(t *testing.T, pool *pgxpool.Pool, sessionID string) {
	t.Helper()
	ctx := context.Background()

	_, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION stall_session_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.id = '%s' THEN PERFORM pg_sleep(1); END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER stall_session_insert AFTER INSERT ON sessions
			FOR EACH ROW EXECUTE FUNCTION stall_session_insert();`, uuid.MustParse(sessionID)))
	require.NoError(t, err)

	t.Cleanup(func() {
		_, err := pool.Exec(ctx, `DROP TRIGGER stall_session_insert ON sessions;
			DROP FUNCTION stall_session_insert();`)
		require.NoError(t, err)
	})
}

// waitForStalledInsert blocks until an insert stalled by stallInsertOf sleeps.
func waitForStalledInsert(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	require.Eventually(t, func() bool {
		var sleeping bool
		err := pool.QueryRow(context.Background(), `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event = 'PgSleep')`).Scan(&sleeping)
		return err == nil && sleeping
	}, 5*time.Second, 10*time.Millisecond)
}
