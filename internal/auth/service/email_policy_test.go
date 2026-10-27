package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
)

func TestRequiredUnverifiedEmailCannotStartSession(t *testing.T) {
	fx := newServiceFixture(t)
	hash, err := service.NewArgon2idHasher(fastParams).Hash("password1")
	require.NoError(t, err)
	user := &domain.User{ID: "user-1", Email: "user@example.com", Username: "user1", PasswordHash: hash, EmailVerificationRequired: true}
	fx.repo.EXPECT().GetByEmail(mock.Anything, user.Email).Return(user, nil)
	// Permit the old behavior so the regression fails on the security invariant,
	// rather than on a missing mock expectation or symbol.
	fx.tokens.EXPECT().IssueAccessToken(user.ID, user.Email, user.Username, mock.Anything).Return("access", time.Now().Add(time.Hour), nil).Maybe()
	fx.tokens.EXPECT().IssueRefreshToken(mock.Anything, user.ID, user.Email, user.Username).Return("refresh", time.Now().Add(time.Hour), nil).Maybe()
	fx.sessions.EXPECT().Create(mock.Anything, mock.Anything).Return(nil).Maybe()
	pair, err := fx.svc.Login(context.Background(), user.Email, "password1")
	require.Error(t, err, "an unverified required account must not receive tokens")
	require.Nil(t, pair)
	fx.sessions.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}
