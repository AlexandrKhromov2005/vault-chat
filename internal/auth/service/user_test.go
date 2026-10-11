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
	f.tokens.EXPECT().Validate("access").Return(&jwt.Claims{TokenType: jwt.AccessToken}, nil).Once()
	f.repo.EXPECT().GetByID(ctx, id).Return(user, nil).Once()
	got, err := f.svc.GetUser(ctx, "access", strings.ToUpper(id))
	require.NoError(t, err)
	require.Same(t, user, got)
	_, err = f.svc.GetUser(ctx, "", id)
	require.ErrorIs(t, err, service.ErrInvalidToken)
	f.tokens.EXPECT().Validate("refresh").Return(&jwt.Claims{TokenType: jwt.RefreshToken}, nil).Once()
	_, err = f.svc.GetUser(ctx, "refresh", id)
	require.ErrorIs(t, err, service.ErrInvalidToken)
}

func TestGetUser_TargetErrors(t *testing.T) {
	ctx := context.Background()
	id := uuid.NewString()
	for _, failure := range []error{repository.ErrNotFound, context.Canceled, errors.New("database down")} {
		f := newServiceFixture(t)
		f.tokens.EXPECT().Validate("access").Return(&jwt.Claims{TokenType: jwt.AccessToken}, nil).Once()
		f.repo.EXPECT().GetByID(ctx, id).Return(nil, failure).Once()
		_, err := f.svc.GetUser(ctx, "access", id)
		if errors.Is(failure, repository.ErrNotFound) {
			require.ErrorIs(t, err, service.ErrUserNotFound)
		} else {
			require.ErrorIs(t, err, failure)
		}
	}
	f := newServiceFixture(t)
	f.tokens.EXPECT().Validate("access").Return(&jwt.Claims{TokenType: jwt.AccessToken}, nil).Once()
	_, err := f.svc.GetUser(ctx, "access", uuid.Nil.String())
	require.ErrorIs(t, err, validator.ErrInvalidUserID)
	require.Empty(t, f.repo.Calls)
}
