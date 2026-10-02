package service_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service/mocks"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/jwt"
)

type serviceFixture struct {
	repo   *mocks.UserRepository
	tokens *mocks.TokenManager
	svc    *service.Service
}

func newServiceFixture(t *testing.T) serviceFixture {
	t.Helper()

	repo := mocks.NewUserRepository(t)
	tokens := mocks.NewTokenManager(t)
	hasher := service.NewArgon2idHasher(fastParams)

	svc, err := service.NewService(repo, hasher, tokens, slog.Default())
	require.NoError(t, err)

	return serviceFixture{repo: repo, tokens: tokens, svc: svc}
}

func TestService_Register(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		fx := newServiceFixture(t)
		fx.repo.EXPECT().
			Create(mock.Anything, mock.AnythingOfType("*domain.User")).
			Return(nil)

		user, err := fx.svc.Register(context.Background(), "user@example.com", "username1", "password1")
		require.NoError(t, err)
		require.NotEmpty(t, user.ID)
		require.Equal(t, "user@example.com", user.Email)
		require.Equal(t, "username1", user.Username)
		require.NotEqual(t, "password1", user.PasswordHash, "password must be stored hashed")
	})

	t.Run("validation errors do not touch the repository", func(t *testing.T) {
		fx := newServiceFixture(t)

		cases := map[string]struct {
			email, username, password string
		}{
			"invalid email":    {"not-an-email", "username1", "password1"},
			"invalid username": {"user@example.com", "1bad", "password1"},
			"weak password":    {"user@example.com", "username1", "short"},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				_, err := fx.svc.Register(context.Background(), tc.email, tc.username, tc.password)
				require.Error(t, err)
			})
		}
	})

	t.Run("duplicate email", func(t *testing.T) {
		fx := newServiceFixture(t)
		fx.repo.EXPECT().Create(mock.Anything, mock.Anything).Return(repository.ErrEmailTaken)

		_, err := fx.svc.Register(context.Background(), "user@example.com", "username1", "password1")
		require.ErrorIs(t, err, service.ErrEmailTaken)
	})

	t.Run("duplicate username", func(t *testing.T) {
		fx := newServiceFixture(t)
		fx.repo.EXPECT().Create(mock.Anything, mock.Anything).Return(repository.ErrUsernameTaken)

		_, err := fx.svc.Register(context.Background(), "user@example.com", "username1", "password1")
		require.ErrorIs(t, err, service.ErrUsernameTaken)
	})

	t.Run("repository failure is wrapped", func(t *testing.T) {
		fx := newServiceFixture(t)
		fx.repo.EXPECT().Create(mock.Anything, mock.Anything).Return(errors.New("connection reset"))

		_, err := fx.svc.Register(context.Background(), "user@example.com", "username1", "password1")
		require.Error(t, err)
		require.NotErrorIs(t, err, service.ErrEmailTaken)
		require.NotErrorIs(t, err, service.ErrUsernameTaken)
	})
}

func TestService_Login(t *testing.T) {
	hasher := service.NewArgon2idHasher(fastParams)
	storedHash, err := hasher.Hash("password1")
	require.NoError(t, err)

	storedUser := &domain.User{
		ID:           "user-1",
		Email:        "user@example.com",
		Username:     "username1",
		PasswordHash: storedHash,
	}

	t.Run("success returns a token pair", func(t *testing.T) {
		fx := newServiceFixture(t)
		fx.repo.EXPECT().GetByEmail(mock.Anything, "user@example.com").Return(storedUser, nil)
		fx.tokens.EXPECT().
			IssueAccessToken("user-1", "user@example.com", "username1").
			Return("access-token", time.Now().Add(15*time.Minute), nil)
		fx.tokens.EXPECT().
			IssueRefreshToken("user-1", "user@example.com", "username1").
			Return("refresh-token", time.Now().Add(720*time.Hour), nil)

		pair, err := fx.svc.Login(context.Background(), "user@example.com", "password1")
		require.NoError(t, err)
		require.Equal(t, "access-token", pair.AccessToken)
		require.Equal(t, "refresh-token", pair.RefreshToken)
		require.False(t, pair.AccessExpiresAt.IsZero())
		require.False(t, pair.RefreshExpiresAt.IsZero())
	})

	t.Run("unknown email yields invalid credentials", func(t *testing.T) {
		fx := newServiceFixture(t)
		fx.repo.EXPECT().GetByEmail(mock.Anything, mock.Anything).Return(nil, repository.ErrNotFound)

		_, err := fx.svc.Login(context.Background(), "ghost@example.com", "password1")
		require.ErrorIs(t, err, service.ErrInvalidCredentials)
	})

	t.Run("wrong password yields invalid credentials", func(t *testing.T) {
		fx := newServiceFixture(t)
		fx.repo.EXPECT().GetByEmail(mock.Anything, mock.Anything).Return(storedUser, nil)

		_, err := fx.svc.Login(context.Background(), "user@example.com", "password2")
		require.ErrorIs(t, err, service.ErrInvalidCredentials)
	})

	t.Run("empty password yields invalid credentials without repository call", func(t *testing.T) {
		fx := newServiceFixture(t)

		_, err := fx.svc.Login(context.Background(), "user@example.com", "")
		require.ErrorIs(t, err, service.ErrInvalidCredentials)
	})

	t.Run("malformed email yields a validation error", func(t *testing.T) {
		fx := newServiceFixture(t)

		_, err := fx.svc.Login(context.Background(), "not-an-email", "password1")
		require.Error(t, err)
		require.NotErrorIs(t, err, service.ErrInvalidCredentials)
	})

	t.Run("repository failure is wrapped", func(t *testing.T) {
		fx := newServiceFixture(t)
		fx.repo.EXPECT().GetByEmail(mock.Anything, mock.Anything).Return(nil, errors.New("connection reset"))

		_, err := fx.svc.Login(context.Background(), "user@example.com", "password1")
		require.Error(t, err)
		require.NotErrorIs(t, err, service.ErrInvalidCredentials)
	})
}

func TestService_ValidateToken(t *testing.T) {
	t.Run("valid token returns claims", func(t *testing.T) {
		fx := newServiceFixture(t)
		want := &jwt.Claims{
			UserID:    "user-1",
			Email:     "user@example.com",
			Username:  "username1",
			TokenType: jwt.AccessToken,
			ExpiresAt: time.Now().Add(time.Hour),
		}
		fx.tokens.EXPECT().Validate("good-token").Return(want, nil)

		claims, err := fx.svc.ValidateToken(context.Background(), "good-token")
		require.NoError(t, err)
		require.Equal(t, want, claims)
	})

	t.Run("invalid token", func(t *testing.T) {
		fx := newServiceFixture(t)
		fx.tokens.EXPECT().Validate("bad-token").Return(nil, errors.New("signature invalid"))

		_, err := fx.svc.ValidateToken(context.Background(), "bad-token")
		require.ErrorIs(t, err, service.ErrInvalidToken)
	})

	t.Run("empty token", func(t *testing.T) {
		fx := newServiceFixture(t)

		_, err := fx.svc.ValidateToken(context.Background(), "")
		require.ErrorIs(t, err, service.ErrInvalidToken)
	})
}
