package service_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/mail"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/jwt"
)

type queuedEmailFlow struct {
	action  *domain.EmailAction
	payload []byte
}

type completedEmailFlow struct {
	action       *domain.EmailAction
	passwordHash string
	notices      [][]byte
}

type emailFlowRepository struct {
	mu                             sync.Mutex
	queued                         []queuedEmailFlow
	completed                      []completedEmailFlow
	actions                        map[string]*domain.EmailAction
	queueErr, findErr, completeErr error
	queueHook                      func(context.Context) error
	findCalls                      int
}

func (*emailFlowRepository) CreateInvited(context.Context, *domain.User, []byte, *domain.EmailAction, []byte) error {
	return nil
}
func (r *emailFlowRepository) QueueAction(ctx context.Context, action *domain.EmailAction, payload []byte) error {
	r.mu.Lock()
	r.queued = append(r.queued, queuedEmailFlow{action: action, payload: append([]byte{}, payload...)})
	if action != nil {
		if r.actions == nil {
			r.actions = make(map[string]*domain.EmailAction)
		}
		r.actions[hex.EncodeToString(action.TokenHash)] = action
	}
	err, hook := r.queueErr, r.queueHook
	r.mu.Unlock()
	if hook != nil {
		return hook(ctx)
	}
	return err
}
func (r *emailFlowRepository) FindAction(_ context.Context, hash []byte, purpose string) (*domain.EmailAction, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.findCalls++
	if r.findErr != nil {
		return nil, r.findErr
	}
	action := r.actions[hex.EncodeToString(hash)]
	if action == nil || action.Purpose != purpose || !action.ExpiresAt.After(time.Now()) {
		return nil, repository.ErrInvalidEmailAction
	}
	return action, nil
}
func (r *emailFlowRepository) CompleteAction(_ context.Context, action *domain.EmailAction, hash string, notices [][]byte) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completed = append(r.completed, completedEmailFlow{action: action, passwordHash: hash, notices: notices})
	if r.completeErr != nil {
		return "", r.completeErr
	}
	return action.UserID, nil
}

func newEmailFlow(t *testing.T) (serviceFixture, *emailFlowRepository, *mail.Box) {
	t.Helper()
	fx := newServiceFixture(t)
	box, err := mail.NewBox(bytes.Repeat([]byte{3}, 32))
	require.NoError(t, err)
	store := &emailFlowRepository{}
	accounts, err := service.NewEmailAccounts(store, box, "https://vault.example.invalid")
	require.NoError(t, err)
	service.WithEmailAccounts(accounts)(fx.svc)
	return fx, store, box
}

func emailFlowUser(t *testing.T, verified bool) *domain.User {
	t.Helper()
	hash, err := service.NewArgon2idHasher(fastParams).Hash("correctpass1")
	require.NoError(t, err)
	user := &domain.User{ID: "22222222-2222-4222-8222-222222222222", Email: "fixture@example.invalid", Username: "fixture", PasswordHash: hash, CredentialVersion: 7, EmailVerificationRequired: true}
	if verified {
		now := time.Now()
		user.EmailVerifiedAt = &now
	}
	return user
}

func authorizeEmailFlow(fx serviceFixture, user *domain.User) {
	fx.tokens.EXPECT().Validate("synthetic-access").Return(&jwt.Claims{UserID: user.ID, SessionID: "11111111-1111-4111-8111-111111111111", TokenType: jwt.AccessToken}, nil)
	fx.repo.EXPECT().GetByID(mock.Anything, user.ID).Return(user, nil)
}

func TestEmailChangePasswordGuessesShareTheAccountLoginBudget(t *testing.T) {
	fx, store, _ := newEmailFlow(t)
	user := emailFlowUser(t, true)
	authorizeEmailFlow(fx, user)
	for attempt := range 6 {
		err := fx.svc.RequestEmailChange(context.Background(), "synthetic-access", "wrongpass1", "new@example.invalid")
		if attempt == 5 {
			require.ErrorIs(t, err, service.ErrLoginRateLimited, "email-change password checks must share the account budget across IPs")
		} else {
			require.ErrorIs(t, err, service.ErrInvalidCredentials)
		}
	}
	require.Empty(t, store.queued, "wrong passwords must never enqueue an ownership challenge")
}

func TestEmailPublicRequestsAreGenericWithoutIssuingSessions(t *testing.T) {
	for _, test := range []struct {
		name, operation                          string
		found, verified, wrongPassword, wantMail bool
	}{
		{name: "reset unknown", operation: "reset"},
		{name: "reset unverified", operation: "reset", found: true},
		{name: "reset verified", operation: "reset", found: true, verified: true, wantMail: true},
		{name: "verify unknown", operation: "verify"},
		{name: "verify wrong password", operation: "verify", found: true, wrongPassword: true},
		{name: "verify already verified", operation: "verify", found: true, verified: true},
		{name: "verify pending", operation: "verify", found: true, wantMail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fx, store, box := newEmailFlow(t)
			user := emailFlowUser(t, test.verified)
			if test.found {
				fx.repo.EXPECT().GetByEmail(mock.Anything, user.Email).Return(user, nil)
			} else {
				fx.repo.EXPECT().GetByEmail(mock.Anything, user.Email).Return(nil, repository.ErrNotFound)
			}
			allowEmailFlowSessionIssuance(fx)
			var err error
			if test.operation == "reset" {
				err = fx.svc.RequestPasswordReset(context.Background(), user.Email)
			} else {
				password := "correctpass1"
				if test.wrongPassword {
					password = "wrongpass1"
				}
				err = fx.svc.RequestEmailVerification(context.Background(), user.Email, password)
			}
			require.NoError(t, err, "public response must not disclose account/password/verification status")
			require.Len(t, store.queued, 1, "even unknown accounts must perform the capacity probe")
			if test.wantMail {
				require.NotNil(t, store.queued[0].action)
				message, err := box.Open(store.queued[0].payload)
				require.NoError(t, err)
				require.Equal(t, user.Email, message.To)
			} else {
				require.Nil(t, store.queued[0].action)
				require.Empty(t, store.queued[0].payload, "generic probes must not enqueue messages to unknown/unowned addresses")
			}
			assertEmailFlowNoSession(t, fx)
		})
	}
}

func allowEmailFlowSessionIssuance(fx serviceFixture) {
	fx.tokens.EXPECT().IssueAccessToken(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("unexpected-access", time.Now().Add(time.Hour), nil).Maybe()
	fx.tokens.EXPECT().IssueRefreshToken(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("unexpected-refresh", time.Now().Add(time.Hour), nil).Maybe()
	fx.sessions.EXPECT().Create(mock.Anything, mock.Anything).Return(nil).Maybe()
}

func assertEmailFlowNoSession(t *testing.T, fx serviceFixture) {
	t.Helper()
	fx.tokens.AssertNotCalled(t, "IssueAccessToken", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	fx.tokens.AssertNotCalled(t, "IssueRefreshToken", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	fx.sessions.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func queuedEmailToken(t *testing.T, box *mail.Box, queued queuedEmailFlow, fragment string) string {
	t.Helper()
	message, err := box.Open(queued.payload)
	require.NoError(t, err)
	for _, word := range strings.Fields(message.Text) {
		link, err := url.Parse(word)
		if err != nil || link.Scheme != "https" || link.Host != "vault.example.invalid" {
			continue
		}
		require.Empty(t, link.RawQuery, "proof must stay out of server access logs and Referer query strings")
		require.True(t, strings.HasPrefix(link.Fragment, fragment+"="), "proof is bound to the expected UI purpose")
		token := strings.TrimPrefix(link.Fragment, fragment+"=")
		require.Len(t, token, 64)
		decoded, err := hex.DecodeString(token)
		require.NoError(t, err)
		require.Len(t, decoded, 32)
		hash := sha256.Sum256([]byte(token))
		require.Equal(t, hash[:], queued.action.TokenHash, "only the digest of the bearer proof may be stored as lookup metadata")
		require.False(t, bytes.Contains(queued.payload, []byte(token)))
		require.False(t, bytes.Contains(queued.payload, []byte(message.To)))
		return token
	}
	t.Fatal("encrypted message lacks the configured application link")
	return ""
}

func TestEmailChallengesBindPurposeVersionTargetAndTTL(t *testing.T) {
	for _, test := range []struct {
		purpose, fragment string
		ttl               time.Duration
	}{
		{"verify", "verify-email", 24 * time.Hour},
		{"reset", "reset-password", 15 * time.Minute},
		{"change", "confirm-email-change", 30 * time.Minute},
	} {
		t.Run(test.purpose, func(t *testing.T) {
			fx, store, box := newEmailFlow(t)
			user := emailFlowUser(t, test.purpose != "verify")
			target := user.Email
			before := time.Now()
			switch test.purpose {
			case "verify":
				fx.repo.EXPECT().GetByEmail(mock.Anything, user.Email).Return(user, nil)
				require.NoError(t, fx.svc.RequestEmailVerification(context.Background(), user.Email, "correctpass1"))
			case "reset":
				fx.repo.EXPECT().GetByEmail(mock.Anything, user.Email).Return(user, nil)
				require.NoError(t, fx.svc.RequestPasswordReset(context.Background(), user.Email))
			case "change":
				authorizeEmailFlow(fx, user)
				target = "new@example.invalid"
				require.NoError(t, fx.svc.RequestEmailChange(context.Background(), "synthetic-access", "correctpass1", target))
			}
			require.Len(t, store.queued, 1)
			queued := store.queued[0]
			require.NotNil(t, queued.action)
			queuedEmailToken(t, box, queued, test.fragment)
			require.Equal(t, user.ID, queued.action.UserID)
			require.Equal(t, user.CredentialVersion, queued.action.CredentialVersion)
			require.Equal(t, user.Email, queued.action.AccountEmail)
			require.Equal(t, target, queued.action.TargetEmail)
			require.Equal(t, test.purpose, queued.action.Purpose)
			require.False(t, queued.action.ExpiresAt.Before(before.Add(test.ttl)))
			require.False(t, queued.action.ExpiresAt.After(time.Now().Add(test.ttl)))
		})
	}
}

func TestEmailCompletionHashesPasswordQueuesNoticesAndNeverLogsIn(t *testing.T) {
	for _, purpose := range []string{"verify", "reset", "change"} {
		t.Run(purpose, func(t *testing.T) {
			fx, store, box := newEmailFlow(t)
			user := emailFlowUser(t, true)
			token := strings.Repeat("a", 64)
			digest := sha256.Sum256([]byte(token))
			action := &domain.EmailAction{ID: "action-fixture", UserID: user.ID, Purpose: purpose, AccountEmail: user.Email, TargetEmail: "new@example.invalid", TokenHash: digest[:], CredentialVersion: user.CredentialVersion, ExpiresAt: time.Now().Add(time.Hour)}
			store.actions = map[string]*domain.EmailAction{hex.EncodeToString(digest[:]): action}
			allowEmailFlowSessionIssuance(fx)
			var id string
			var err error
			switch purpose {
			case "verify":
				id, err = fx.svc.ConfirmEmailVerification(context.Background(), token)
			case "reset":
				id, err = fx.svc.ConfirmPasswordReset(context.Background(), token, "newpassword2")
			case "change":
				id, err = fx.svc.ConfirmEmailChange(context.Background(), token)
			}
			require.NoError(t, err)
			require.Equal(t, user.ID, id)
			require.Len(t, store.completed, 1)
			completed := store.completed[0]
			require.Equal(t, action, completed.action, "transaction must consume the proof that was looked up")
			if purpose == "reset" {
				require.NotEqual(t, "newpassword2", completed.passwordHash)
				ok, err := service.NewArgon2idHasher(fastParams).Verify("newpassword2", completed.passwordHash)
				require.NoError(t, err)
				require.True(t, ok, "the committed hash must authenticate the requested new password")
			} else {
				require.Empty(t, completed.passwordHash)
			}
			notices := 0
			if purpose == "reset" {
				notices = 1
			}
			if purpose == "change" {
				notices = 2
			}
			require.Len(t, completed.notices, notices)
			for index, payload := range completed.notices {
				message, err := box.Open(payload)
				require.NoError(t, err)
				expected := user.Email
				if index == 1 {
					expected = action.TargetEmail
				}
				require.Equal(t, expected, message.To)
				require.NotContains(t, message.Text, token)
				require.NotContains(t, message.Text, "newpassword2")
			}
			assertEmailFlowNoSession(t, fx)
		})
	}
}

func TestEmailChangeAndLoginUseTheSameAccountBudget(t *testing.T) {
	fx, store, _ := newEmailFlow(t)
	user := emailFlowUser(t, true)
	authorizeEmailFlow(fx, user)
	fx.repo.EXPECT().GetByEmail(mock.Anything, user.Email).Return(user, nil).Times(5)
	for range 5 {
		_, err := fx.svc.Login(context.Background(), user.Email, "wrongpass1")
		require.ErrorIs(t, err, service.ErrInvalidCredentials)
	}
	require.ErrorIs(t, fx.svc.RequestEmailChange(context.Background(), "synthetic-access", "wrongpass1", "new@example.invalid"), service.ErrLoginRateLimited)
	require.Empty(t, store.queued)
}

type countingEmailFlowHasher struct {
	base   service.Hasher
	hashes atomic.Int32
}

func (h *countingEmailFlowHasher) Hash(password string) (string, error) {
	h.hashes.Add(1)
	return h.base.Hash(password)
}
func (h *countingEmailFlowHasher) Verify(password, hash string) (bool, error) {
	return h.base.Verify(password, hash)
}

func TestEmailRejectedProofsDoNotHashPasswordsOrIssueSessions(t *testing.T) {
	for _, test := range []struct {
		name, token, purpose            string
		expired                         bool
		findErr, completeErr, errorWant error
		wantHashes                      int32
	}{
		{name: "short token", token: "bad", errorWant: service.ErrInvalidEmailAction, wantHashes: 1},
		{name: "nonhex token", token: strings.Repeat("x", 64), errorWant: service.ErrInvalidEmailAction, wantHashes: 1},
		{name: "missing", token: strings.Repeat("a", 64), errorWant: service.ErrInvalidEmailAction, wantHashes: 1},
		{name: "expired", token: strings.Repeat("a", 64), purpose: "reset", expired: true, errorWant: service.ErrInvalidEmailAction, wantHashes: 1},
		{name: "wrong purpose", token: strings.Repeat("a", 64), purpose: "verify", errorWant: service.ErrInvalidEmailAction, wantHashes: 1},
		{name: "lookup unavailable", token: strings.Repeat("a", 64), findErr: errors.New("synthetic database outage"), errorWant: service.ErrEmailUnavailable, wantHashes: 1},
		{name: "concurrent consumption", token: strings.Repeat("a", 64), purpose: "reset", completeErr: repository.ErrInvalidEmailAction, errorWant: service.ErrInvalidEmailAction, wantHashes: 2},
		{name: "commit unavailable", token: strings.Repeat("a", 64), purpose: "reset", completeErr: errors.New("synthetic commit outage"), errorWant: service.ErrEmailUnavailable, wantHashes: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			fx, store, box := newEmailFlow(t)
			hasher := &countingEmailFlowHasher{base: service.NewArgon2idHasher(fastParams)}
			var err error
			fx.svc, err = service.NewService(fx.repo, fx.sessions, hasher, fx.tokens, slog.Default())
			require.NoError(t, err)
			accounts, err := service.NewEmailAccounts(store, box, "https://vault.example.invalid")
			require.NoError(t, err)
			service.WithEmailAccounts(accounts)(fx.svc)
			allowEmailFlowSessionIssuance(fx)
			store.findErr, store.completeErr = test.findErr, test.completeErr
			if test.purpose != "" {
				digest := sha256.Sum256([]byte(test.token))
				expiry := time.Now().Add(time.Hour)
				if test.expired {
					expiry = time.Now().Add(-time.Second)
				}
				store.actions = map[string]*domain.EmailAction{hex.EncodeToString(digest[:]): {
					ID: "proof-fixture", UserID: "fixture-user", Purpose: test.purpose, AccountEmail: "fixture@example.invalid", TargetEmail: "fixture@example.invalid", TokenHash: digest[:], ExpiresAt: expiry,
				}}
			}
			id, err := fx.svc.ConfirmPasswordReset(context.Background(), test.token, "newpassword2")
			require.ErrorIs(t, err, test.errorWant)
			require.Empty(t, id)
			require.Equal(t, test.wantHashes, hasher.hashes.Load(), "malformed/expired/wrong-purpose proofs must fail before another KDF allocation")
			require.NotContains(t, err.Error(), "database")
			require.NotContains(t, err.Error(), "commit")
			if test.wantHashes == 1 {
				require.Empty(t, store.completed)
			}
			assertEmailFlowNoSession(t, fx)
		})
	}
}

func TestEmailInvalidPasswordIsRejectedBeforeConsumingProof(t *testing.T) {
	fx, store, _ := newEmailFlow(t)
	_, err := fx.svc.ConfirmPasswordReset(context.Background(), strings.Repeat("a", 64), "weak")
	require.Error(t, err)
	require.Zero(t, store.findCalls)
	require.Empty(t, store.completed)
}

func TestEmailAccountInfoLoadsCurrentPrivateIdentity(t *testing.T) {
	fx, _, _ := newEmailFlow(t)
	user := emailFlowUser(t, false)
	authorizeEmailFlow(fx, user)
	info, err := fx.svc.GetAccount(context.Background(), "synthetic-access")
	require.NoError(t, err)
	require.Equal(t, user.ID, info.UserID)
	require.Equal(t, user.Email, info.Email)
	require.Equal(t, user.Username, info.Username)
	require.False(t, info.EmailVerified)
	require.True(t, info.VerificationRequired)
}

func TestEmailDisabledWorkflowsFailClosed(t *testing.T) {
	fx := newServiceFixture(t)
	require.ErrorIs(t, fx.svc.RequestPasswordReset(context.Background(), "fixture@example.invalid"), service.ErrEmailUnavailable)
	require.ErrorIs(t, fx.svc.RequestEmailVerification(context.Background(), "fixture@example.invalid", "password1"), service.ErrEmailUnavailable)
	require.ErrorIs(t, fx.svc.RequestEmailChange(context.Background(), "synthetic-access", "password1", "new@example.invalid"), service.ErrEmailUnavailable)
	_, err := fx.svc.ConfirmEmailVerification(context.Background(), strings.Repeat("a", 64))
	require.ErrorIs(t, err, service.ErrEmailUnavailable)
}

func TestEmailRequestsNormalizeQueueErrorsAndReleaseCapacity(t *testing.T) {
	for _, queueErr := range []error{nil, repository.ErrEmailActionRateLimited, repository.ErrInvalidEmailAction, errors.New("synthetic database outage")} {
		t.Run(map[bool]string{true: "outage", false: "normal"}[queueErr != nil]+"/"+errorNameEmailFlow(queueErr), func(t *testing.T) {
			fx, store, _ := newEmailFlow(t)
			user := emailFlowUser(t, true)
			fx.repo.EXPECT().GetByEmail(mock.Anything, user.Email).Return(user, nil)
			store.queueErr = queueErr
			err := fx.svc.RequestPasswordReset(context.Background(), user.Email)
			if queueErr != nil && !errors.Is(queueErr, repository.ErrEmailActionRateLimited) && !errors.Is(queueErr, repository.ErrInvalidEmailAction) {
				require.ErrorIs(t, err, service.ErrEmailUnavailable)
				require.NotContains(t, err.Error(), "database")
			} else {
				require.NoError(t, err)
			}
			store.queueErr = nil
			require.NoError(t, fx.svc.RequestPasswordReset(context.Background(), user.Email), "a failed request must release its admission slot")
		})
	}
}

func errorNameEmailFlow(err error) string {
	if err == nil {
		return "none"
	}
	return err.Error()
}

func TestEmailRequestCapacityRejectsOverflowAndRecovers(t *testing.T) {
	for _, outcome := range []error{nil, errors.New("synthetic queue outage")} {
		t.Run(map[bool]string{false: "success", true: "outage"}[outcome != nil], func(t *testing.T) {
			assertEmailFlowCapacityRecovers(t, outcome)
		})
	}
}

func assertEmailFlowCapacityRecovers(t *testing.T, outcome error) {
	t.Helper()
	fx, store, _ := newEmailFlow(t)
	user := emailFlowUser(t, true)
	fx.repo.EXPECT().GetByEmail(mock.Anything, user.Email).Return(user, nil)
	var active atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	store.queueHook = func(ctx context.Context) error {
		if active.Add(1) == 32 {
			close(entered)
		}
		select {
		case <-release:
			return outcome
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	results := make(chan error, 32)
	for range 32 {
		go func() { results <- fx.svc.RequestPasswordReset(ctx, user.Email) }()
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("requests never occupied the admission budget")
	}
	require.ErrorIs(t, fx.svc.RequestPasswordReset(ctx, user.Email), service.ErrEmailUnavailable)
	once.Do(func() { close(release) })
	for range 32 {
		if outcome == nil {
			require.NoError(t, <-results)
		} else {
			require.ErrorIs(t, <-results, service.ErrEmailUnavailable)
		}
	}
	store.queueHook = nil
	require.NoError(t, fx.svc.RequestPasswordReset(ctx, user.Email), "all request slots must be reusable after completion")
}

func TestEmailConfigurationAllowsOnlySafeApplicationOrigins(t *testing.T) {
	box, err := mail.NewBox(bytes.Repeat([]byte{5}, 32))
	require.NoError(t, err)
	for _, origin := range []string{"https://vault.example.invalid", "https://vault.example.invalid/", "http://127.0.0.1:8080", "http://[::1]:8080", "http://localhost:8080"} {
		_, err := service.NewEmailAccounts(&emailFlowRepository{}, box, origin)
		require.NoError(t, err, origin)
	}
	for _, origin := range []string{"", "http://vault.example.invalid", "http://localhost.evil.invalid", "ftp://vault.example.invalid", "https://user:password@vault.example.invalid", "https://vault.example.invalid/path", "https://vault.example.invalid/?query=token", "https://vault.example.invalid/#fragment"} {
		_, err := service.NewEmailAccounts(&emailFlowRepository{}, box, origin)
		require.ErrorIs(t, err, service.ErrEmailUnavailable, origin)
	}
	_, err = service.NewEmailAccounts(nil, box, "https://vault.example.invalid")
	require.ErrorIs(t, err, service.ErrEmailUnavailable)
	_, err = service.NewEmailAccounts(&emailFlowRepository{}, nil, "https://vault.example.invalid")
	require.ErrorIs(t, err, service.ErrEmailUnavailable)
}
