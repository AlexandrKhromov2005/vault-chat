package integration

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
	servicemocks "github.com/AlexandrKhromov2005/vault-chat/internal/auth/service/mocks"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/jwt"
)

func TestRotatedRefreshFromOldCredentialEpochCannotRevokeNewSessions(t *testing.T) {
	pool, emails, user := emailFixture(t, true)
	ctx := context.Background()
	sessions := repository.NewSessionRepository(pool)
	old := newTestSession(user.ID, time.Hour)
	require.NoError(t, sessions.Create(ctx, old))
	rotated := newTestSession(user.ID, time.Hour)
	require.NoError(t, sessions.Rotate(ctx, old.ID, rotated))

	// Recovery revokes both old refresh rows and advances the credential epoch.
	proof := emailAction(user, "reset")
	require.NoError(t, emails.QueueAction(ctx, proof, emailPayload(t, user.Email)))
	_, err := emails.CompleteAction(ctx, proof, "synthetic-new-password-hash", [][]byte{emailPayload(t, user.Email)})
	require.NoError(t, err)
	current := newTestSession(user.ID, time.Hour)
	current.CredentialVersion = 1
	require.NoError(t, sessions.Create(ctx, current))

	// A stolen, already-rotated JWT remains cryptographically valid until its
	// expiry, but must have no authority over a login made after recovery.
	tokens := servicemocks.NewMockTokenManager(t)
	tokens.EXPECT().Validate("synthetic-old-rotated-refresh").Return(&jwt.Claims{
		ID: old.ID, UserID: user.ID, TokenType: jwt.RefreshToken,
	}, nil)
	tokens.EXPECT().IssueAccessToken(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return("synthetic-access", time.Now().Add(time.Minute), nil).Maybe()
	tokens.EXPECT().IssueRefreshToken(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return("synthetic-refresh", time.Now().Add(time.Hour), nil).Maybe()
	hasher := service.NewArgon2idHasher(service.Argon2idParams{MemoryKiB: 1024, Time: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
	svc, err := service.NewService(repository.NewUserRepository(pool), sessions, hasher, tokens, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	pair, err := svc.RefreshToken(ctx, "synthetic-old-rotated-refresh")
	require.ErrorIs(t, err, service.ErrInvalidToken)
	require.Nil(t, pair)
	active, err := sessions.IsActive(ctx, user.ID, current.ID)
	require.NoError(t, err)
	require.True(t, active, "a refresh token from before recovery must not revoke a session created with the new credential epoch")
}
