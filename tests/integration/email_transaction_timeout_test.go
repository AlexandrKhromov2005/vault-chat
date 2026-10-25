package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEmailTransactionUserLockWaitHasAnInternalDeadline(t *testing.T) {
	pool, emails, user := emailFixture(t, true)
	setup, stopSetup := context.WithTimeout(t.Context(), 10*time.Second)
	defer stopSetup()
	blocker, err := pool.Begin(setup)
	require.NoError(t, err)
	unlock := func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = blocker.Rollback(cleanup)
	}
	defer unlock()
	_, err = blocker.Exec(setup, `SELECT pg_advisory_xact_lock(hashtextextended('auth.sessions:' || ($1::uuid)::text,0))`, user.ID)
	require.NoError(t, err)

	// The repository promises its own five-second transaction budget, even
	// when an internal caller supplies no deadline and the user lock is busy.
	operation, cancelOperation := context.WithCancel(context.Background())
	defer cancelOperation()
	_, hasDeadline := operation.Deadline()
	require.False(t, hasDeadline)
	action := emailAction(user, "reset")
	payload := emailPayload(t, user.Email)
	result := make(chan error, 1)
	started := time.Now()
	go func() { result <- emails.QueueAction(operation, action, payload) }()

	var operationErr error
	finishedWithinBudget := false
	timer := time.NewTimer(7 * time.Second)
	defer timer.Stop()
	select {
	case operationErr = <-result:
		finishedWithinBudget = true
	case <-timer.C:
	}
	elapsed := time.Since(started)
	// Cleanup precedes the assertion so a reproduced failure cannot leave a
	// blocked goroutine, transaction or advisory lock behind in the fixture.
	cancelOperation()
	unlock()
	if !finishedWithinBudget {
		select {
		case operationErr = <-result:
		case <-time.After(5 * time.Second):
			t.Fatal("cancelled synthetic email transaction did not finish cleanup")
		}
	}
	require.True(t, finishedWithinBudget, "the email transaction ignored its five-second deadline while waiting for the user lock")
	require.ErrorIs(t, operationErr, context.DeadlineExceeded)
	require.GreaterOrEqual(t, elapsed, 4*time.Second, "the held user lock must exercise the transaction deadline")
}
