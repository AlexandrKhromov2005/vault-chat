package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/jwt"
)

func TestGetUser_RequiresAccessTokenAndCanonicalizesTarget(t *testing.T) {
	ctx := context.Background()
	f := newServiceFixture(t)
	id := uuid.NewString()
	user := &domain.User{ID: id, Username: "bob"}
	f.tokens.EXPECT().Validate("access").Return(&jwt.Claims{SessionID: "11111111-1111-4111-8111-111111111111", TokenType: jwt.AccessToken}, nil).Once()
	f.repo.EXPECT().GetByID(ctx, id).Return(user, nil).Once()
	got, err := f.svc.GetUser(ctx, "access", strings.ToUpper(id))
	require.NoError(t, err)
	require.Same(t, user, got)
	_, err = f.svc.GetUser(ctx, "", id)
	require.ErrorIs(t, err, service.ErrInvalidToken)
	f.tokens.EXPECT().Validate("refresh").Return(&jwt.Claims{SessionID: "11111111-1111-4111-8111-111111111111", TokenType: jwt.RefreshToken}, nil).Once()
	_, err = f.svc.GetUser(ctx, "refresh", id)
	require.ErrorIs(t, err, service.ErrInvalidToken)
}

func TestGetUser_TargetErrors(t *testing.T) {
	ctx := context.Background()
	id := uuid.NewString()
	for _, failure := range []error{repository.ErrNotFound, context.Canceled, errors.New("database down")} {
		f := newServiceFixture(t)
		f.tokens.EXPECT().Validate("access").Return(&jwt.Claims{SessionID: "11111111-1111-4111-8111-111111111111", TokenType: jwt.AccessToken}, nil).Once()
		f.repo.EXPECT().GetByID(ctx, id).Return(nil, failure).Once()
		_, err := f.svc.GetUser(ctx, "access", id)
		if errors.Is(failure, repository.ErrNotFound) {
			require.ErrorIs(t, err, service.ErrUserNotFound)
		} else {
			require.ErrorIs(t, err, failure)
		}
	}
	f := newServiceFixture(t)
	f.tokens.EXPECT().Validate("access").Return(&jwt.Claims{SessionID: "11111111-1111-4111-8111-111111111111", TokenType: jwt.AccessToken}, nil).Once()
	_, err := f.svc.GetUser(ctx, "access", uuid.Nil.String())
	require.ErrorIs(t, err, validator.ErrInvalidUserID)
	require.Empty(t, f.repo.Calls)
}

func TestSearchUsers_ExcludesCallerAndAppliesDefaultLimit(t *testing.T) {
	ctx := context.Background()
	f := newServiceFixture(t)
	users := []*domain.User{{ID: uuid.NewString(), Username: "bob"}}
	f.tokens.EXPECT().Validate("access").Return(&jwt.Claims{SessionID: "11111111-1111-4111-8111-111111111111", TokenType: jwt.AccessToken, UserID: "caller"}, nil).Once()
	f.repo.EXPECT().SearchByUsernamePrefix(ctx, "Bo", "caller", 20).Return(users, nil).Once()
	got, err := f.svc.SearchUsers(ctx, "access", "Bo", 0)
	require.NoError(t, err)
	require.Equal(t, users, got)
}

func TestSearchUsers_RejectsBeforeLookup(t *testing.T) {
	access := &jwt.Claims{SessionID: "11111111-1111-4111-8111-111111111111", TokenType: jwt.AccessToken, UserID: "caller"}
	cases := []struct {
		name, token, query string
		limit              int32
		claims             *jwt.Claims
		err                error
	}{
		{"no token", "", "bo", 0, nil, service.ErrInvalidToken},
		{"refresh token", "refresh", "bo", 0, &jwt.Claims{SessionID: "11111111-1111-4111-8111-111111111111", TokenType: jwt.RefreshToken}, service.ErrInvalidToken},
		{"wildcard query", "access", "b%", 0, access, validator.ErrInvalidUsernameQuery},
		{"empty query", "access", "", 0, access, validator.ErrInvalidUsernameQuery},
		{"single character query", "access", "b", 0, access, validator.ErrInvalidUsernameQuery},
		{"limit too large", "access", "bo", 51, access, validator.ErrInvalidSearchLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceFixture(t)
			if tc.claims != nil {
				f.tokens.EXPECT().Validate(tc.token).Return(tc.claims, nil).Once()
			}
			_, err := f.svc.SearchUsers(context.Background(), tc.token, tc.query, tc.limit)
			require.ErrorIs(t, err, tc.err)
			require.Empty(t, f.repo.Calls)
		})
	}
}

func TestSearchUsers_WrapsStorageFailure(t *testing.T) {
	ctx := context.Background()
	f := newServiceFixture(t)
	failure := errors.New("database down")
	f.tokens.EXPECT().Validate("access").Return(&jwt.Claims{SessionID: "11111111-1111-4111-8111-111111111111", TokenType: jwt.AccessToken, UserID: "caller"}, nil).Once()
	f.repo.EXPECT().SearchByUsernamePrefix(ctx, "bo", "caller", 5).Return(nil, failure).Once()
	_, err := f.svc.SearchUsers(ctx, "access", "bo", 5)
	require.ErrorIs(t, err, failure)
}
