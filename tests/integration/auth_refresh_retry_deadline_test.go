package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
)

// Admission before a deadline does not authorize a retry after a lock wait.
// Both the reuse window and the successor's lifetime use the database clock
// after the per-user lock has been acquired.
func TestSessionRepository_RetryChecksDeadlinesAfterLockWait(t *testing.T) {
	for _, deadline := range []string{"retry window", "successor expiry"} {
		t.Run(deadline, func(t *testing.T) {
			pool, repo, user := newSessionFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			current := newTestSession(user.ID, time.Hour)
			require.NoError(t, repo.Create(ctx, current))
			next := newTestSession(user.ID, time.Hour)
			_, err := repo.Rotate(ctx, current.ID, next, time.Minute)
			require.NoError(t, err)

			blocker, err := pool.Begin(ctx)
			require.NoError(t, err)
			defer func() { _ = blocker.Rollback(context.Background()) }()
			_, err = blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('auth.sessions:' || ($1::uuid)::text,0))`, user.ID)
			require.NoError(t, err)

			// Establish a deadline long enough to observe the waiter entering
			// its transaction, without depending on application clock skew.
			const window = 2 * time.Second
			retryWindow := time.Hour
			if deadline == "retry window" {
				retryWindow = window
				_, err = pool.Exec(ctx, `UPDATE sessions SET revoked_at=clock_timestamp() WHERE id=$1::uuid`, current.ID)
			} else {
				_, err = pool.Exec(ctx, `UPDATE sessions SET expires_at=clock_timestamp()+interval '2 seconds' WHERE id=$1::uuid`, next.ID)
			}
			require.NoError(t, err)

			finished := make(chan error, 1)
			go func() {
				_, rotateErr := repo.Rotate(ctx, current.ID, newTestSession(user.ID, time.Hour), retryWindow)
				finished <- rotateErr
			}()
			require.Eventually(t, func() bool {
				var waiting bool
				queryErr := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
WHERE datname=current_database() AND wait_event='advisory' AND pid<>pg_backend_pid())`).Scan(&waiting)
				return queryErr == nil && waiting
			}, time.Second, 10*time.Millisecond)
			_, err = blocker.Exec(ctx, `SELECT pg_sleep(2.1)`)
			require.NoError(t, err)
			require.NoError(t, blocker.Commit(ctx))
			require.ErrorIs(t, <-finished, repository.ErrSessionReused,
				"a deadline crossed while waiting for the user lock must reject the replay")
		})
	}
}

func TestSessionRepository_RetryWindowStartsAtRotationAfterLockWait(t *testing.T) {
	pool, repo, user := newSessionFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	current := newTestSession(user.ID, time.Hour)
	require.NoError(t, repo.Create(ctx, current))
	next := newTestSession(user.ID, time.Hour)

	blocker, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = blocker.Rollback(context.Background()) }()
	_, err = blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('auth.sessions:' || ($1::uuid)::text,0))`, user.ID)
	require.NoError(t, err)

	const window = 2 * time.Second
	finished := make(chan error, 1)
	go func() {
		_, rotateErr := repo.Rotate(ctx, current.ID, next, window)
		finished <- rotateErr
	}()
	require.Eventually(t, func() bool {
		var waiting bool
		queryErr := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
WHERE datname=current_database() AND wait_event='advisory' AND pid<>pg_backend_pid())`).Scan(&waiting)
		return queryErr == nil && waiting
	}, time.Second, 10*time.Millisecond)
	_, err = blocker.Exec(ctx, `SELECT pg_sleep(2.1)`)
	require.NoError(t, err)
	require.NoError(t, blocker.Commit(ctx))
	require.NoError(t, <-finished)

	active, err := repo.Rotate(ctx, current.ID, newTestSession(user.ID, time.Hour), window)
	require.NoError(t, err, "lock waiting before rotation must not consume the retry window")
	require.Equal(t, next.ID, active.ID)
}
