package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/mail"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
)

var _ mail.Queue = (*repository.EmailRepository)(nil)

func emailPayload(t *testing.T, email string) []byte {
	t.Helper()
	box, err := mail.NewBox(bytes.Repeat([]byte{42}, 32))
	require.NoError(t, err)
	payload, err := box.Seal(mail.Message{To: email, Subject: "Synthetic account proof", Text: "synthetic-token-only"})
	require.NoError(t, err)
	return payload
}

func emailAction(user *domain.User, purpose string) *domain.EmailAction {
	hash := sha256.Sum256([]byte(uuid.NewString()))
	ttl := 15 * time.Minute
	switch purpose {
	case "verify":
		ttl = 24 * time.Hour
	case "change":
		ttl = 30 * time.Minute
	}
	return &domain.EmailAction{ID: uuid.NewString(), UserID: user.ID, Purpose: purpose, AccountEmail: user.Email,
		TargetEmail: user.Email, TokenHash: hash[:], CredentialVersion: user.CredentialVersion, ExpiresAt: time.Now().Add(ttl)}
}

func emailFixture(t *testing.T, verified bool) (*pgxpool.Pool, *repository.EmailRepository, *domain.User) {
	t.Helper()
	pool := newTestPool(t)
	user := newTestUser()
	require.NoError(t, repository.NewUserRepository(pool).Create(context.Background(), user))
	if verified {
		now := time.Now()
		user.EmailVerifiedAt = &now
		_, err := pool.Exec(context.Background(), `UPDATE users SET email_verified_at=clock_timestamp() WHERE id=$1`, user.ID)
		require.NoError(t, err)
	}
	return pool, repository.NewEmailRepository(pool), user
}

func TestEmailAccountRegistrationRequiresVerification(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	user := newTestUser()
	user.EmailVerificationRequired = true
	hash := sha256.Sum256([]byte("synthetic-invitation"))
	_, err := pool.Exec(ctx, `INSERT INTO registration_invitations(token_hash,email,expires_at) VALUES($1,$2,$3)`, hash[:], user.Email, time.Now().Add(time.Hour))
	require.NoError(t, err)
	action := emailAction(user, "verify")
	// The old invitation path reproduced RED by dropping the required flag.
	repo := repository.NewEmailRepository(pool)
	require.NoError(t, repo.CreateInvited(ctx, user, hash[:], action, emailPayload(t, user.Email)))
	var required, verified bool
	var queued int
	require.NoError(t, pool.QueryRow(ctx, `SELECT email_verification_required,email_verified_at IS NOT NULL FROM users WHERE id=$1`, user.ID).Scan(&required, &verified))
	require.True(t, required, "mail-enabled registration must remain pending until mailbox ownership is proved")
	require.False(t, verified)
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox WHERE action_id=$1`, action.ID).Scan(&queued))
	require.Equal(t, 1, queued)
	_, err = repo.FindAction(ctx, action.TokenHash, "verify")
	require.NoError(t, err)
	require.ErrorIs(t, repo.CreateInvited(ctx, newTestUser(), hash[:], action, emailPayload(t, user.Email)), repository.ErrInvalidEmailAction)
}

func rejectEmailInserts(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	name := "reject_email_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{name}.Sanitize()
	_, err := pool.Exec(context.Background(), fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'synthetic outbox insertion failure'; END $$;
CREATE TRIGGER %s BEFORE INSERT ON email_outbox FOR EACH ROW EXECUTE FUNCTION %s()`, quoted, quoted, quoted))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER %s ON email_outbox; DROP FUNCTION %s()`, quoted, quoted))
		require.NoError(t, err)
	})
}

func TestEmailRegistrationRollsBackInvitationOnOutboxFailure(t *testing.T) {
	pool := newTestPool(t)
	user := newTestUser()
	action := emailAction(user, "verify")
	hash := sha256.Sum256([]byte("synthetic-invitation"))
	_, err := pool.Exec(context.Background(), `INSERT INTO registration_invitations(token_hash,email,expires_at) VALUES($1,$2,clock_timestamp()+interval '1 hour')`, hash[:], user.Email)
	require.NoError(t, err)
	rejectEmailInserts(t, pool)
	require.Error(t, repository.NewEmailRepository(pool).CreateInvited(context.Background(), user, hash[:], action, emailPayload(t, user.Email)))
	var users, actions, queue int
	var consumed bool
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM users),(SELECT count(*) FROM email_actions),(SELECT count(*) FROM email_outbox),used_at IS NOT NULL FROM registration_invitations WHERE token_hash=$1`, hash[:]).Scan(&users, &actions, &queue, &consumed))
	require.Zero(t, users)
	require.Zero(t, actions)
	require.Zero(t, queue)
	require.False(t, consumed)
}

func TestEmailActionsRejectWrongPurposeBindingAndUnverifiedReset(t *testing.T) {
	pool, repo, user := emailFixture(t, false)
	ctx := context.Background()
	action := emailAction(user, "reset")
	require.ErrorIs(t, repo.QueueAction(ctx, action, emailPayload(t, user.Email)), repository.ErrInvalidEmailAction)
	action = emailAction(user, "verify")
	require.NoError(t, repo.QueueAction(ctx, action, emailPayload(t, user.Email)))
	_, err := repo.FindAction(ctx, action.TokenHash, "reset")
	require.ErrorIs(t, err, repository.ErrInvalidEmailAction)
	for _, edit := range []func(*domain.EmailAction){
		func(a *domain.EmailAction) { a.UserID = uuid.NewString() },
		func(a *domain.EmailAction) { a.TargetEmail = "elsewhere@example.invalid" },
		func(a *domain.EmailAction) { a.CredentialVersion++ },
		func(a *domain.EmailAction) { a.TokenHash = []byte("malformed") },
	} {
		changed := *action
		edit(&changed)
		_, err = repo.CompleteAction(ctx, &changed, "", nil)
		require.ErrorIs(t, err, repository.ErrInvalidEmailAction)
	}
	_, err = repo.CompleteAction(ctx, action, "", nil)
	require.NoError(t, err)
	_, err = repo.CompleteAction(ctx, action, "", nil)
	require.ErrorIs(t, err, repository.ErrInvalidEmailAction)
	var verified bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT email_verified_at IS NOT NULL FROM users WHERE id=$1`, user.ID).Scan(&verified))
	require.True(t, verified)
}

func TestEmailResetHasOneConcurrentConsumerAndRevokesEverySession(t *testing.T) {
	pool, repo, user := emailFixture(t, true)
	ctx := context.Background()
	action := emailAction(user, "reset")
	require.NoError(t, repo.QueueAction(ctx, action, emailPayload(t, user.Email)))
	sessions := repository.NewSessionRepository(pool)
	current := newTestSession(user.ID, time.Hour)
	require.NoError(t, sessions.Create(ctx, current))
	notice := emailPayload(t, user.Email)
	start := make(chan struct{})
	errs := make(chan error, 8)
	for range 8 {
		go func() {
			<-start
			_, err := repo.CompleteAction(ctx, action, "synthetic-new-password-hash", [][]byte{notice})
			errs <- err
		}()
	}
	close(start)
	winners := 0
	for range 8 {
		err := <-errs
		if err == nil {
			winners++
		} else {
			require.ErrorIs(t, err, repository.ErrInvalidEmailAction)
		}
	}
	require.Equal(t, 1, winners)
	var hash string
	var version int64
	var active, queued int
	require.NoError(t, pool.QueryRow(ctx, `SELECT password_hash,credential_version FROM users WHERE id=$1`, user.ID).Scan(&hash, &version))
	require.Equal(t, "synthetic-new-password-hash", hash)
	require.EqualValues(t, 1, version)
	require.NoError(t, pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM sessions WHERE revoked_at IS NULL),(SELECT count(*) FROM email_outbox)`).Scan(&active, &queued))
	require.Zero(t, active)
	require.Equal(t, 1, queued)
	_, err := repo.FindAction(ctx, action.TokenHash, "reset")
	require.ErrorIs(t, err, repository.ErrInvalidEmailAction)
}

func TestEmailResetRequestKeepsOlderLinksUntilSuccessfulConsumption(t *testing.T) {
	pool, repo, user := emailFixture(t, true)
	ctx := context.Background()
	first := emailAction(user, "reset")
	require.NoError(t, repo.QueueAction(ctx, first, emailPayload(t, user.Email)))
	_, err := pool.Exec(ctx, `UPDATE email_actions SET created_at=clock_timestamp()-interval '2 minutes' WHERE id=$1`, first.ID)
	require.NoError(t, err)
	second := emailAction(user, "reset")
	require.NoError(t, repo.QueueAction(ctx, second, emailPayload(t, user.Email)))
	_, err = repo.FindAction(ctx, first.TokenHash, "reset")
	require.NoError(t, err)
	_, err = repo.CompleteAction(ctx, first, "new-hash", [][]byte{emailPayload(t, user.Email)})
	require.NoError(t, err)
	_, err = repo.FindAction(ctx, second.TokenHash, "reset")
	require.ErrorIs(t, err, repository.ErrInvalidEmailAction)
}

func TestEmailCredentialMutationRollsBackWhenNoticeCannotBeStored(t *testing.T) {
	pool, repo, user := emailFixture(t, true)
	ctx := context.Background()
	action := emailAction(user, "reset")
	require.NoError(t, repo.QueueAction(ctx, action, emailPayload(t, user.Email)))
	session := newTestSession(user.ID, time.Hour)
	require.NoError(t, repository.NewSessionRepository(pool).Create(ctx, session))
	rejectEmailInserts(t, pool)
	_, err := repo.CompleteAction(ctx, action, "new-hash", [][]byte{emailPayload(t, user.Email)})
	require.Error(t, err)
	var hash string
	var version int64
	var consumed, revoked bool
	var queued int
	require.NoError(t, pool.QueryRow(ctx, `SELECT password_hash,credential_version,(SELECT consumed_at IS NOT NULL FROM email_actions WHERE id=$2),
(SELECT revoked_at IS NOT NULL FROM sessions WHERE id=$3),(SELECT count(*) FROM email_outbox) FROM users WHERE id=$1`, user.ID, action.ID, session.ID).Scan(&hash, &version, &consumed, &revoked, &queued))
	require.Equal(t, user.PasswordHash, hash)
	require.Zero(t, version)
	require.False(t, consumed)
	require.False(t, revoked)
	require.Equal(t, 1, queued)
}

func fillEmailQueue(t *testing.T, pool *pgxpool.Pool, count int, payload []byte) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `INSERT INTO email_outbox(id,payload,expires_at)
SELECT gen_random_uuid(),$1,clock_timestamp()+interval '1 hour' FROM generate_series(1,$2::integer)`, payload, count)
	require.NoError(t, err)
}

func TestEmailQueueCapacityIsGlobalAndIncludesLeasesAndRetries(t *testing.T) {
	pool, repo, user := emailFixture(t, true)
	ctx := context.Background()
	payload := emailPayload(t, user.Email)
	fillEmailQueue(t, pool, 999, payload)
	_, err := pool.Exec(ctx, `UPDATE email_outbox SET lease=gen_random_uuid(),lease_until=clock_timestamp()+interval '1 minute',available_at=clock_timestamp()+interval '5 minutes'`)
	require.NoError(t, err)
	other := newTestUser()
	require.NoError(t, repository.NewUserRepository(pool).Create(ctx, other))
	_, err = pool.Exec(ctx, `UPDATE users SET email_verified_at=clock_timestamp() WHERE id=$1`, other.ID)
	require.NoError(t, err)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, u := range []*domain.User{user, other} {
		go func() { <-start; results <- repo.QueueAction(ctx, emailAction(u, "reset"), payload) }()
	}
	close(start)
	first, second := <-results, <-results
	if first != nil {
		first, second = second, first
	}
	require.NoError(t, first)
	require.ErrorIs(t, second, repository.ErrEmailCapacity)
	var queued int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox`).Scan(&queued))
	require.Equal(t, 1000, queued)
	require.ErrorIs(t, repo.QueueAction(ctx, nil, nil), repository.ErrEmailCapacity)
	unknown := emailAction(newTestUser(), "reset")
	require.ErrorIs(t, repo.QueueAction(ctx, unknown, payload), repository.ErrEmailCapacity)
}

func TestEmailHourlyQuotaSurvivesConsumptionAndPayloadDeletion(t *testing.T) {
	pool, repo, user := emailFixture(t, true)
	ctx := context.Background()
	for range 5 {
		action := emailAction(user, "reset")
		require.NoError(t, repo.QueueAction(ctx, action, emailPayload(t, user.Email)))
		_, err := pool.Exec(ctx, `UPDATE email_actions SET created_at=clock_timestamp()-interval '2 minutes',consumed_at=clock_timestamp(); DELETE FROM email_outbox`)
		require.NoError(t, err)
	}
	require.ErrorIs(t, repo.QueueAction(ctx, emailAction(user, "reset"), emailPayload(t, user.Email)), repository.ErrEmailActionRateLimited)
	require.NoError(t, repo.QueueAction(ctx, nil, nil), "unknown-account probes have no account quota")
}

func TestEmailChangeOccupiedTargetIsGenericAndCountsItsRequest(t *testing.T) {
	pool, repo, user := emailFixture(t, true)
	ctx := context.Background()
	other := newTestUser()
	require.NoError(t, repository.NewUserRepository(pool).Create(ctx, other))
	action := emailAction(user, "change")
	action.TargetEmail = strings.ToUpper(other.Email)
	require.NoError(t, repo.QueueAction(ctx, action, emailPayload(t, action.TargetEmail)))
	var queued, requests int
	var consumed bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM email_outbox),(SELECT count(*) FROM email_actions),consumed_at IS NOT NULL FROM email_actions WHERE id=$1`, action.ID).Scan(&queued, &requests, &consumed))
	require.Zero(t, queued)
	require.Equal(t, 1, requests)
	require.True(t, consumed)
	_, err := repo.FindAction(ctx, action.TokenHash, "change")
	require.ErrorIs(t, err, repository.ErrInvalidEmailAction)
	next := emailAction(user, "change")
	next.TargetEmail = "free@example.invalid"
	require.ErrorIs(t, repo.QueueAction(ctx, next, emailPayload(t, next.TargetEmail)), repository.ErrEmailActionRateLimited)
}

func TestEmailChangeCaseInsensitiveFinalCollisionRollsBack(t *testing.T) {
	pool, repo, user := emailFixture(t, true)
	ctx := context.Background()
	action := emailAction(user, "change")
	action.TargetEmail = "wanted@example.invalid"
	require.NoError(t, repo.QueueAction(ctx, action, emailPayload(t, action.TargetEmail)))
	other := newTestUser()
	other.Email = strings.ToUpper(action.TargetEmail)
	require.NoError(t, repository.NewUserRepository(pool).Create(ctx, other))
	_, err := repo.CompleteAction(ctx, action, "", [][]byte{emailPayload(t, user.Email), emailPayload(t, action.TargetEmail)})
	require.ErrorIs(t, err, repository.ErrInvalidEmailAction)
	got, err := repository.NewUserRepository(pool).GetByID(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, user.Email, got.Email)
	require.Zero(t, got.CredentialVersion)
	_, err = repo.FindAction(ctx, action.TokenHash, "change")
	require.NoError(t, err)
}

func TestEmailActionExpiryIsRecheckedAfterWaitingForUserLock(t *testing.T) {
	pool, repo, user := emailFixture(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	action := emailAction(user, "reset")
	require.NoError(t, repo.QueueAction(ctx, action, emailPayload(t, user.Email)))
	blocker, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = blocker.Rollback(context.Background()) }()
	_, err = blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('auth.sessions:' || ($1::uuid)::text,0))`, user.ID)
	require.NoError(t, err)
	done := make(chan error, 1)
	notice := emailPayload(t, user.Email)
	go func() { _, err := repo.CompleteAction(ctx, action, "new-hash", [][]byte{notice}); done <- err }()
	require.Eventually(t, func() bool {
		var waiting bool
		err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory' AND query LIKE '%auth.sessions:%' AND state='active')`).Scan(&waiting)
		return err == nil && waiting
	}, 5*time.Second, 10*time.Millisecond)
	// Expiry is after the consumer transaction began, but before it gets the
	// lock. PostgreSQL now() would still consider this proof valid afterwards.
	_, err = pool.Exec(ctx, `UPDATE email_actions SET expires_at=clock_timestamp() WHERE id=$1`, action.ID)
	require.NoError(t, err)
	require.NoError(t, blocker.Rollback(ctx))
	require.ErrorIs(t, <-done, repository.ErrInvalidEmailAction)
	got, err := repository.NewUserRepository(pool).GetByID(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, user.PasswordHash, got.PasswordHash)
	require.Zero(t, got.CredentialVersion)
}

func TestEmailOutboxLeasesFenceStaleAcknowledgementsAndBoundAttempts(t *testing.T) {
	pool, repo, user := emailFixture(t, true)
	ctx := context.Background()
	fillEmailQueue(t, pool, 1, emailPayload(t, user.Email))
	first, err := repo.Claim(ctx)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Equal(t, 1, first.Attempts)
	second, err := repo.Claim(ctx)
	require.NoError(t, err)
	require.Nil(t, second, "a leased row cannot be claimed twice")
	current := first
	for attempt := 2; attempt <= 5; attempt++ {
		_, err = pool.Exec(ctx, `UPDATE email_outbox SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, first.ID)
		require.NoError(t, err)
		current, err = repo.Claim(ctx)
		require.NoError(t, err)
		require.NotNil(t, current)
		require.Equal(t, attempt, current.Attempts)
		require.NotEqual(t, first.Lease, current.Lease)
		require.NoError(t, repo.Complete(ctx, first.ID, first.Lease))
		require.NoError(t, repo.Retry(ctx, first.ID, first.Lease, time.Time{}))
		var left int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox WHERE id=$1`, first.ID).Scan(&left))
		require.Equal(t, 1, left)
	}
	_, err = pool.Exec(ctx, `UPDATE email_outbox SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, first.ID)
	require.NoError(t, err)
	last, err := repo.Claim(ctx)
	require.NoError(t, err)
	require.Nil(t, last, "crashed fifth attempt must not send a sixth time")
	var left int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox`).Scan(&left))
	require.Zero(t, left)
}

func TestEmailOutboxRetryAndCompletionDeleteOnlyOwnedLease(t *testing.T) {
	pool, repo, user := emailFixture(t, true)
	ctx := context.Background()
	fillEmailQueue(t, pool, 2, emailPayload(t, user.Email))
	first, err := repo.Claim(ctx)
	require.NoError(t, err)
	require.NotNil(t, first)
	second, err := repo.Claim(ctx)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.NoError(t, repo.Retry(ctx, first.ID, first.Lease, time.Now().Add(time.Minute)))
	require.NoError(t, repo.Complete(ctx, second.ID, second.Lease))
	next, err := repo.Claim(ctx)
	require.NoError(t, err)
	require.Nil(t, next, "retry must respect available_at")
	_, err = pool.Exec(ctx, `UPDATE email_outbox SET available_at=clock_timestamp()-interval '1 second' WHERE id=$1`, first.ID)
	require.NoError(t, err)
	next, err = repo.Claim(ctx)
	require.NoError(t, err)
	require.NotNil(t, next)
	require.Equal(t, 2, next.Attempts)
	require.NoError(t, repo.Retry(ctx, next.ID, next.Lease, time.Time{}))
	var left int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox`).Scan(&left))
	require.Zero(t, left)
}

func TestEmailActionExpiryUsesDatabaseClockWithoutMutatingCaller(t *testing.T) {
	pool, repo, user := emailFixture(t, true)
	ctx := context.Background()
	action := emailAction(user, "reset")
	action.ExpiresAt = action.ExpiresAt.Add(time.Minute) // Simulate an application clock ahead of the database.
	supplied := action.ExpiresAt
	require.NoError(t, repo.QueueAction(ctx, action, emailPayload(t, user.Email)))
	require.Equal(t, supplied, action.ExpiresAt)
	var actionExpiry, outboxExpiry, maximum time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT a.expires_at,o.expires_at,clock_timestamp()+interval '15 minutes'
FROM email_actions a JOIN email_outbox o ON o.action_id=a.id WHERE a.id=$1`, action.ID).Scan(&actionExpiry, &outboxExpiry, &maximum))
	require.True(t, actionExpiry.Before(maximum))
	require.True(t, actionExpiry.Equal(outboxExpiry))
}

func TestEmailFindRejectsActionBoundToSupersededAccountState(t *testing.T) {
	pool, repo, user := emailFixture(t, true)
	ctx := context.Background()
	action := emailAction(user, "reset")
	require.NoError(t, repo.QueueAction(ctx, action, emailPayload(t, user.Email)))
	_, err := pool.Exec(ctx, `UPDATE users SET credential_version=credential_version+1 WHERE id=$1`, user.ID)
	require.NoError(t, err)
	_, err = repo.FindAction(ctx, action.TokenHash, "reset")
	require.ErrorIs(t, err, repository.ErrInvalidEmailAction, "reject stale proofs before password hashing")
}

func TestEmailChangeCommitsVerifiedIdentityAndBothNotices(t *testing.T) {
	pool, repo, user := emailFixture(t, true)
	ctx := context.Background()
	reset := emailAction(user, "reset")
	require.NoError(t, repo.QueueAction(ctx, reset, emailPayload(t, user.Email)))
	change := emailAction(user, "change")
	change.TargetEmail = "new-owner@example.invalid"
	require.NoError(t, repo.QueueAction(ctx, change, emailPayload(t, change.TargetEmail)))
	session := newTestSession(user.ID, time.Hour)
	require.NoError(t, repository.NewSessionRepository(pool).Create(ctx, session))
	id, err := repo.CompleteAction(ctx, change, "", [][]byte{emailPayload(t, user.Email), emailPayload(t, change.TargetEmail)})
	require.NoError(t, err)
	require.Equal(t, user.ID, id)
	got, err := repository.NewUserRepository(pool).GetByID(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, change.TargetEmail, got.Email)
	require.NotNil(t, got.EmailVerifiedAt)
	require.EqualValues(t, 1, got.CredentialVersion)
	_, err = repo.FindAction(ctx, reset.TokenHash, "reset")
	require.ErrorIs(t, err, repository.ErrInvalidEmailAction)
	active, err := repository.NewSessionRepository(pool).IsActive(ctx, user.ID, session.ID)
	require.NoError(t, err)
	require.False(t, active)
	rows, err := pool.Query(ctx, `SELECT payload FROM email_outbox WHERE action_id IS NULL`)
	require.NoError(t, err)
	defer rows.Close()
	box, err := mail.NewBox(bytes.Repeat([]byte{42}, 32))
	require.NoError(t, err)
	var recipients []string
	for rows.Next() {
		var payload []byte
		require.NoError(t, rows.Scan(&payload))
		message, err := box.Open(payload)
		require.NoError(t, err)
		recipients = append(recipients, message.To)
	}
	require.NoError(t, rows.Err())
	require.ElementsMatch(t, []string{user.Email, change.TargetEmail}, recipients)
}

func TestEmailChangeReservesBothNoticesBeforeMutatingAccount(t *testing.T) {
	pool, repo, user := emailFixture(t, true)
	ctx := context.Background()
	change := emailAction(user, "change")
	change.TargetEmail = "new-owner@example.invalid"
	payload := emailPayload(t, user.Email)
	require.NoError(t, repo.QueueAction(ctx, change, payload))
	fillEmailQueue(t, pool, 999, payload)
	notices := [][]byte{payload, emailPayload(t, change.TargetEmail)}
	_, err := repo.CompleteAction(ctx, change, "", notices)
	require.ErrorIs(t, err, repository.ErrEmailCapacity)
	got, err := repository.NewUserRepository(pool).GetByID(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, user.Email, got.Email)
	require.Zero(t, got.CredentialVersion)
	_, err = repo.FindAction(ctx, change.TokenHash, "change")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM email_outbox WHERE id=(SELECT id FROM email_outbox WHERE action_id IS NULL LIMIT 1)`)
	require.NoError(t, err)
	_, err = repo.CompleteAction(ctx, change, "", notices)
	require.NoError(t, err)
	var queued int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox`).Scan(&queued))
	require.Equal(t, 1000, queued)
}

func TestEmailOutboxSkipsLockedRowsAndRetainsRecentRequestAudit(t *testing.T) {
	pool, repo, user := emailFixture(t, true)
	ctx := context.Background()
	fillEmailQueue(t, pool, 2, emailPayload(t, user.Email))
	blocker, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = blocker.Rollback(ctx) }()
	var lockedID string
	require.NoError(t, blocker.QueryRow(ctx, `SELECT id::text FROM email_outbox ORDER BY created_at,id LIMIT 1 FOR UPDATE`).Scan(&lockedID))
	claimCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	job, err := repo.Claim(claimCtx)
	require.NoError(t, err)
	require.NotNil(t, job)
	require.NotEqual(t, lockedID, job.ID)
	require.NoError(t, blocker.Rollback(ctx))
	recent, old := emailAction(user, "reset"), emailAction(user, "verify")
	for _, test := range []struct {
		action *domain.EmailAction
		age    string
	}{{recent, "2 hours"}, {old, "25 hours"}} {
		_, err = pool.Exec(ctx, `INSERT INTO email_actions(id,token_hash,user_id,purpose,account_email,target_email,credential_version,expires_at,created_at,consumed_at)
VALUES($1,$2,$3,$4,$5,$5,0,clock_timestamp()-interval '1 minute',clock_timestamp()-$6::interval,clock_timestamp())`, test.action.ID, test.action.TokenHash, user.ID, test.action.Purpose, user.Email, test.age)
		require.NoError(t, err)
	}
	_, err = repo.Claim(ctx)
	require.NoError(t, err)
	var recentLeft, oldLeft int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE id=$1),count(*) FILTER(WHERE id=$2) FROM email_actions`, recent.ID, old.ID).Scan(&recentLeft, &oldLeft))
	require.Equal(t, 1, recentLeft)
	require.Zero(t, oldLeft)
}

func TestEmailAdoptedDeploymentRejectsLegacyInvitationRegistration(t *testing.T) {
	pool, _, enrolled := emailFixture(t, false)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `UPDATE users SET email_verification_required=true WHERE id=$1`, enrolled.ID)
	require.NoError(t, err)
	user := newTestUser()
	hash := sha256.Sum256([]byte("synthetic legacy invitation after adoption"))
	_, err = pool.Exec(ctx, `INSERT INTO registration_invitations(token_hash,email,expires_at) VALUES($1,$2,clock_timestamp()+interval '1 hour')`, hash[:], user.Email)
	require.NoError(t, err)
	err = repository.NewUserRepository(pool).CreateInvited(ctx, user, hash[:])
	var registered, consumed bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1),used_at IS NOT NULL
FROM registration_invitations WHERE token_hash=$2`, user.ID, hash[:]).Scan(&registered, &consumed))
	require.ErrorIs(t, err, repository.ErrEmailPolicyRequired, "disabling mail delivery must not reopen legacy registration after ownership policy adoption")
	require.False(t, registered)
	require.False(t, consumed)
}

func TestEmailVerificationEnrollsLegacyAccountBeyondItsGracePeriod(t *testing.T) {
	pool, repo, user := emailFixture(t, false)
	ctx := context.Background()
	action := emailAction(user, "verify")
	require.NoError(t, repo.QueueAction(ctx, action, emailPayload(t, user.Email)))
	_, err := repo.CompleteAction(ctx, action, "", nil)
	require.NoError(t, err)
	var enrolled bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT email_verification_required FROM users WHERE id=$1`, user.ID).Scan(&enrolled))
	require.True(t, enrolled, "verification graduates a legacy account into persisted ownership policy")
}

func TestEmailFirstEnrollmentSerializesLegacyInvitationRegistration(t *testing.T) {
	pool := newTestPool(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	first := newTestUser()
	enrollment, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = enrollment.Rollback(cleanup)
	}()
	_, err = enrollment.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('auth.email.outbox',0))`)
	require.NoError(t, err)
	_, err = enrollment.Exec(ctx, `INSERT INTO users(id,email,username,password_hash,email_verification_required) VALUES($1,$2,$3,$4,true)`, first.ID, first.Email, first.Username, first.PasswordHash)
	require.NoError(t, err)
	legacy := newTestUser()
	hash := sha256.Sum256([]byte("synthetic legacy invitation racing first enrollment"))
	_, err = pool.Exec(ctx, `INSERT INTO registration_invitations(token_hash,email,expires_at) VALUES($1,$2,clock_timestamp()+interval '1 hour')`, hash[:], legacy.Email)
	require.NoError(t, err)
	result := make(chan error, 1)
	go func() { result <- repository.NewUserRepository(pool).CreateInvited(ctx, legacy, hash[:]) }()
	require.Eventually(t, func() bool {
		var waiting bool
		err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND state='active' AND wait_event='advisory' AND query LIKE '%auth.email.outbox%')`).Scan(&waiting)
		return err == nil && waiting
	}, time.Second, 10*time.Millisecond, "legacy registration must wait for the first enrollment transaction")
	require.NoError(t, enrollment.Commit(ctx))
	require.ErrorIs(t, <-result, repository.ErrEmailPolicyRequired)
	var consumed, registered bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT used_at IS NOT NULL,EXISTS(SELECT 1 FROM users WHERE id=$1) FROM registration_invitations WHERE token_hash=$2`, legacy.ID, hash[:]).Scan(&consumed, &registered))
	require.False(t, consumed)
	require.False(t, registered)
}
