package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/jwt"
)

func refreshClaims() *jwt.Claims {
	return &jwt.Claims{
		ID:        "session-1",
		UserID:    "user-1",
		Email:     "user@example.com",
		Username:  "username1",
		TokenType: jwt.RefreshToken,
		ExpiresAt: time.Now().Add(time.Hour),
	}
}

func TestService_RefreshToken(t *testing.T) {
	// The identity is reloaded on refresh, so it may differ from the claims.
	user := &domain.User{ID: "user-1", Email: "renamed@example.com", Username: "renamed1"}

	// expectRotation sets up a valid refresh token for a live user up to the
	// point where the session is rotated.
	expectRotation := func(fx serviceFixture) *string {
		fx.tokens.EXPECT().Validate("refresh").Return(refreshClaims(), nil)
		fx.repo.EXPECT().GetByID(mock.Anything, "user-1").Return(user, nil)
		return fx.expectTokenPair(user)
	}

	t.Run("success rotates the session and issues a pair for the current identity", func(t *testing.T) {
		fx := newServiceFixture(t)
		sessionID := expectRotation(fx)
		fx.sessions.EXPECT().
			Rotate(mock.Anything, "session-1", mock.MatchedBy(func(s *domain.Session) bool {
				return s.ID == *sessionID && s.ID != "session-1" && s.UserID == "user-1" &&
					s.ExpiresAt.Equal(refreshExpiresAt)
			})).
			Return(nil)

		pair, err := fx.svc.RefreshToken(context.Background(), "refresh")
		require.NoError(t, err)
		require.Equal(t, "user-1", pair.UserID)
		require.Equal(t, "access-token", pair.AccessToken)
		require.Equal(t, "refresh-token", pair.RefreshToken)
		require.Equal(t, refreshExpiresAt, pair.RefreshExpiresAt)
	})

	t.Run("empty token", func(t *testing.T) {
		fx := newServiceFixture(t)

		_, err := fx.svc.RefreshToken(context.Background(), "")
		require.ErrorIs(t, err, service.ErrInvalidToken)
	})

	t.Run("invalid token", func(t *testing.T) {
		fx := newServiceFixture(t)
		fx.tokens.EXPECT().Validate("refresh").Return(nil, errors.New("signature invalid"))

		_, err := fx.svc.RefreshToken(context.Background(), "refresh")
		require.ErrorIs(t, err, service.ErrInvalidToken)
	})

	t.Run("access token cannot be used as a refresh token", func(t *testing.T) {
		fx := newServiceFixture(t)
		claims := refreshClaims()
		claims.TokenType = jwt.AccessToken
		fx.tokens.EXPECT().Validate("refresh").Return(claims, nil)

		_, err := fx.svc.RefreshToken(context.Background(), "refresh")
		require.ErrorIs(t, err, service.ErrInvalidToken)
	})

	t.Run("deleted user", func(t *testing.T) {
		fx := newServiceFixture(t)
		fx.tokens.EXPECT().Validate("refresh").Return(refreshClaims(), nil)
		fx.repo.EXPECT().GetByID(mock.Anything, "user-1").Return(nil, repository.ErrNotFound)

		_, err := fx.svc.RefreshToken(context.Background(), "refresh")
		require.ErrorIs(t, err, service.ErrInvalidToken)
	})

	t.Run("unknown or expired session", func(t *testing.T) {
		fx := newServiceFixture(t)
		expectRotation(fx)
		fx.sessions.EXPECT().Rotate(mock.Anything, "session-1", mock.Anything).Return(repository.ErrNotFound)

		_, err := fx.svc.RefreshToken(context.Background(), "refresh")
		require.ErrorIs(t, err, service.ErrInvalidToken)
	})

	t.Run("logged-out session is rejected without touching other sessions", func(t *testing.T) {
		fx := newServiceFixture(t)
		expectRotation(fx)
		fx.sessions.EXPECT().Rotate(mock.Anything, "session-1", mock.Anything).Return(repository.ErrSessionRevoked)

		_, err := fx.svc.RefreshToken(context.Background(), "refresh")
		require.ErrorIs(t, err, service.ErrInvalidToken)
	})

	t.Run("reuse of a rotated token revokes every session of the user", func(t *testing.T) {
		fx := newServiceFixture(t)
		expectRotation(fx)
		fx.sessions.EXPECT().Rotate(mock.Anything, "session-1", mock.Anything).Return(repository.ErrSessionReused)
		fx.sessions.EXPECT().RevokeAllForUser(mock.Anything, "user-1").Return(3, nil)

		_, err := fx.svc.RefreshToken(context.Background(), "refresh")
		require.ErrorIs(t, err, service.ErrInvalidToken)
	})

	t.Run("failure to contain token reuse is reported as an internal error", func(t *testing.T) {
		fx := newServiceFixture(t)
		expectRotation(fx)
		fx.sessions.EXPECT().Rotate(mock.Anything, "session-1", mock.Anything).Return(repository.ErrSessionReused)
		fx.sessions.EXPECT().RevokeAllForUser(mock.Anything, "user-1").Return(0, errors.New("connection reset"))

		_, err := fx.svc.RefreshToken(context.Background(), "refresh")
		require.Error(t, err)
		require.NotErrorIs(t, err, service.ErrInvalidToken)
	})

	t.Run("repository failure is wrapped", func(t *testing.T) {
		fx := newServiceFixture(t)
		expectRotation(fx)
		fx.sessions.EXPECT().Rotate(mock.Anything, mock.Anything, mock.Anything).Return(errors.New("connection reset"))

		_, err := fx.svc.RefreshToken(context.Background(), "refresh")
		require.Error(t, err)
		require.NotErrorIs(t, err, service.ErrInvalidToken)
	})
}

func TestService_Logout(t *testing.T) {
	t.Run("success revokes the session of the token", func(t *testing.T) {
		fx := newServiceFixture(t)
		fx.tokens.EXPECT().Validate("refresh").Return(refreshClaims(), nil)
		fx.sessions.EXPECT().Revoke(mock.Anything, "session-1").Return(nil)

		require.NoError(t, fx.svc.Logout(context.Background(), "refresh"))
	})

	t.Run("empty token", func(t *testing.T) {
		fx := newServiceFixture(t)

		err := fx.svc.Logout(context.Background(), "")
		require.ErrorIs(t, err, service.ErrInvalidToken)
	})

	t.Run("access token is rejected", func(t *testing.T) {
		fx := newServiceFixture(t)
		claims := refreshClaims()
		claims.TokenType = jwt.AccessToken
		fx.tokens.EXPECT().Validate("access").Return(claims, nil)

		err := fx.svc.Logout(context.Background(), "access")
		require.ErrorIs(t, err, service.ErrInvalidToken)
	})

	t.Run("invalid token is rejected", func(t *testing.T) {
		fx := newServiceFixture(t)
		fx.tokens.EXPECT().Validate("bad").Return(nil, errors.New("signature invalid"))

		err := fx.svc.Logout(context.Background(), "bad")
		require.ErrorIs(t, err, service.ErrInvalidToken)
	})

	t.Run("repository failure is wrapped", func(t *testing.T) {
		fx := newServiceFixture(t)
		fx.tokens.EXPECT().Validate("refresh").Return(refreshClaims(), nil)
		fx.sessions.EXPECT().Revoke(mock.Anything, "session-1").Return(errors.New("connection reset"))

		err := fx.svc.Logout(context.Background(), "refresh")
		require.Error(t, err)
		require.NotErrorIs(t, err, service.ErrInvalidToken)
	})
}

func TestService_RevokeAllSessions(t *testing.T) {
	const userID = "3f2b8c1e-9d4a-4e6b-8f7c-2a1d0e9b8c7d"

	t.Run("success returns the number of revoked sessions", func(t *testing.T) {
		fx := newServiceFixture(t)
		fx.sessions.EXPECT().RevokeAllForUser(mock.Anything, userID).Return(2, nil)

		revoked, err := fx.svc.RevokeAllSessions(context.Background(), userID)
		require.NoError(t, err)
		require.EqualValues(t, 2, revoked)
	})

	t.Run("malformed user id does not touch the repository", func(t *testing.T) {
		fx := newServiceFixture(t)

		_, err := fx.svc.RevokeAllSessions(context.Background(), "user-1")
		require.ErrorIs(t, err, validator.ErrInvalidUserID)
	})

	t.Run("repository failure is wrapped", func(t *testing.T) {
		fx := newServiceFixture(t)
		fx.sessions.EXPECT().RevokeAllForUser(mock.Anything, userID).Return(0, errors.New("connection reset"))

		_, err := fx.svc.RevokeAllSessions(context.Background(), userID)
		require.Error(t, err)
		require.NotErrorIs(t, err, validator.ErrInvalidUserID)
	})
}
