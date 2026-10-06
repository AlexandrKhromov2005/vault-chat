package handler_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/handler/mocks"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/jwt"
)

func TestAuthGRPCHandler_Register(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		svc := mocks.NewMockAuthService(t)
		h := handler.NewAuthGRPCHandler(svc)

		svc.EXPECT().
			Register(mock.Anything, "user@example.com", "username1", "password1").
			Return(&domain.User{ID: "user-1", Email: "user@example.com", Username: "username1"}, nil)

		resp, err := h.Register(context.Background(), &authv1.RegisterRequest{
			Email:    "user@example.com",
			Username: "username1",
			Password: "password1",
		})
		require.NoError(t, err)
		require.Equal(t, "user-1", resp.GetUserId())
		require.Equal(t, "user@example.com", resp.GetEmail())
		require.Equal(t, "username1", resp.GetUsername())
	})

	t.Run("validation failure maps to InvalidArgument", func(t *testing.T) {
		svc := mocks.NewMockAuthService(t)
		h := handler.NewAuthGRPCHandler(svc)

		svc.EXPECT().Register(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, fmt.Errorf("register: %w", validator.ErrInvalidEmail))

		_, err := h.Register(context.Background(), &authv1.RegisterRequest{})
		require.Equal(t, codes.InvalidArgument, status.Code(err))
		require.Equal(t, "invalid email address", status.Convert(err).Message(),
			"internal error context must not leak to clients")
	})

	t.Run("duplicate email maps to AlreadyExists", func(t *testing.T) {
		svc := mocks.NewMockAuthService(t)
		h := handler.NewAuthGRPCHandler(svc)

		svc.EXPECT().Register(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, service.ErrEmailTaken)

		_, err := h.Register(context.Background(), &authv1.RegisterRequest{})
		require.Equal(t, codes.AlreadyExists, status.Code(err))
	})

	t.Run("duplicate username maps to AlreadyExists", func(t *testing.T) {
		svc := mocks.NewMockAuthService(t)
		h := handler.NewAuthGRPCHandler(svc)

		svc.EXPECT().Register(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, service.ErrUsernameTaken)

		_, err := h.Register(context.Background(), &authv1.RegisterRequest{})
		require.Equal(t, codes.AlreadyExists, status.Code(err))
	})

	t.Run("internal failure maps to Internal", func(t *testing.T) {
		svc := mocks.NewMockAuthService(t)
		h := handler.NewAuthGRPCHandler(svc)

		svc.EXPECT().Register(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("db down"))

		_, err := h.Register(context.Background(), &authv1.RegisterRequest{})
		require.Equal(t, codes.Internal, status.Code(err))
	})
}

func TestAuthGRPCHandler_Login(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		svc := mocks.NewMockAuthService(t)
		h := handler.NewAuthGRPCHandler(svc)

		accessExp := time.Now().Add(15 * time.Minute)
		refreshExp := time.Now().Add(720 * time.Hour)
		svc.EXPECT().Login(mock.Anything, "user@example.com", "password1").
			Return(&service.TokenPair{
				UserID:           "user-1",
				AccessToken:      "access-token",
				RefreshToken:     "refresh-token",
				AccessExpiresAt:  accessExp,
				RefreshExpiresAt: refreshExp,
			}, nil)

		resp, err := h.Login(context.Background(), &authv1.LoginRequest{
			Email:    "user@example.com",
			Password: "password1",
		})
		require.NoError(t, err)
		require.Equal(t, "user-1", resp.GetUserId())
		require.Equal(t, "access-token", resp.GetAccessToken())
		require.Equal(t, "refresh-token", resp.GetRefreshToken())
		require.Equal(t, accessExp.Unix(), resp.GetAccessExpiresAt().GetSeconds())
		require.Equal(t, refreshExp.Unix(), resp.GetRefreshExpiresAt().GetSeconds())
	})

	t.Run("bad credentials map to Unauthenticated", func(t *testing.T) {
		svc := mocks.NewMockAuthService(t)
		h := handler.NewAuthGRPCHandler(svc)

		svc.EXPECT().Login(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, service.ErrInvalidCredentials)

		_, err := h.Login(context.Background(), &authv1.LoginRequest{})
		require.Equal(t, codes.Unauthenticated, status.Code(err))
	})

	t.Run("internal failure maps to Internal", func(t *testing.T) {
		svc := mocks.NewMockAuthService(t)
		h := handler.NewAuthGRPCHandler(svc)

		svc.EXPECT().Login(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("db down"))

		_, err := h.Login(context.Background(), &authv1.LoginRequest{})
		require.Equal(t, codes.Internal, status.Code(err))
	})
}

func TestAuthGRPCHandler_ValidateToken(t *testing.T) {
	t.Run("valid token", func(t *testing.T) {
		svc := mocks.NewMockAuthService(t)
		h := handler.NewAuthGRPCHandler(svc)

		expiresAt := time.Now().Add(15 * time.Minute)
		svc.EXPECT().ValidateToken(mock.Anything, "good-token").
			Return(&jwt.Claims{
				UserID:    "user-1",
				Email:     "user@example.com",
				Username:  "username1",
				TokenType: jwt.AccessToken,
				ExpiresAt: expiresAt,
			}, nil)

		resp, err := h.ValidateToken(context.Background(), &authv1.ValidateTokenRequest{Token: "good-token"})
		require.NoError(t, err)
		require.True(t, resp.GetValid())
		require.Equal(t, "user-1", resp.GetUserId())
		require.Equal(t, "user@example.com", resp.GetEmail())
		require.Equal(t, "username1", resp.GetUsername())
		require.Equal(t, expiresAt.Unix(), resp.GetExpiresAt().GetSeconds())
	})

	t.Run("invalid token yields valid=false without an error", func(t *testing.T) {
		svc := mocks.NewMockAuthService(t)
		h := handler.NewAuthGRPCHandler(svc)

		svc.EXPECT().ValidateToken(mock.Anything, "bad-token").
			Return(nil, service.ErrInvalidToken)

		resp, err := h.ValidateToken(context.Background(), &authv1.ValidateTokenRequest{Token: "bad-token"})
		require.NoError(t, err)
		require.False(t, resp.GetValid())
	})

	t.Run("internal failure maps to Internal", func(t *testing.T) {
		svc := mocks.NewMockAuthService(t)
		h := handler.NewAuthGRPCHandler(svc)

		svc.EXPECT().ValidateToken(mock.Anything, mock.Anything).
			Return(nil, errors.New("db down"))

		_, err := h.ValidateToken(context.Background(), &authv1.ValidateTokenRequest{Token: "any"})
		require.Equal(t, codes.Internal, status.Code(err))
	})
}
