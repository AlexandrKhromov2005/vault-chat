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
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/handler/mocks"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
)

func TestAuthGRPCHandler_RefreshToken(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		svc := mocks.NewMockAuthService(t)
		h := handler.NewAuthGRPCHandler(svc)

		accessExp := time.Now().Add(15 * time.Minute)
		refreshExp := time.Now().Add(720 * time.Hour)
		svc.EXPECT().RefreshToken(mock.Anything, "refresh-token").
			Return(&service.TokenPair{
				UserID:           "user-1",
				AccessToken:      "new-access-token",
				RefreshToken:     "new-refresh-token",
				AccessExpiresAt:  accessExp,
				RefreshExpiresAt: refreshExp,
			}, nil)

		resp, err := h.RefreshToken(context.Background(), &authv1.RefreshTokenRequest{RefreshToken: "refresh-token"})
		require.NoError(t, err)
		require.Equal(t, "user-1", resp.GetUserId())
		require.Equal(t, "new-access-token", resp.GetAccessToken())
		require.Equal(t, "new-refresh-token", resp.GetRefreshToken())
		require.Equal(t, accessExp.Unix(), resp.GetAccessExpiresAt().GetSeconds())
		require.Equal(t, refreshExp.Unix(), resp.GetRefreshExpiresAt().GetSeconds())
	})

	t.Run("invalid token maps to Unauthenticated", func(t *testing.T) {
		svc := mocks.NewMockAuthService(t)
		h := handler.NewAuthGRPCHandler(svc)

		svc.EXPECT().RefreshToken(mock.Anything, mock.Anything).Return(nil, service.ErrInvalidToken)

		_, err := h.RefreshToken(context.Background(), &authv1.RefreshTokenRequest{})
		require.Equal(t, codes.Unauthenticated, status.Code(err))
	})

	t.Run("internal failure maps to Internal", func(t *testing.T) {
		svc := mocks.NewMockAuthService(t)
		h := handler.NewAuthGRPCHandler(svc)

		svc.EXPECT().RefreshToken(mock.Anything, mock.Anything).Return(nil, errors.New("db down"))

		_, err := h.RefreshToken(context.Background(), &authv1.RefreshTokenRequest{})
		require.Equal(t, codes.Internal, status.Code(err))
	})
}

func TestAuthGRPCHandler_Logout(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		svc := mocks.NewMockAuthService(t)
		h := handler.NewAuthGRPCHandler(svc)

		svc.EXPECT().Logout(mock.Anything, "refresh-token").Return(nil)

		resp, err := h.Logout(context.Background(), &authv1.LogoutRequest{RefreshToken: "refresh-token"})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("invalid token maps to Unauthenticated", func(t *testing.T) {
		svc := mocks.NewMockAuthService(t)
		h := handler.NewAuthGRPCHandler(svc)

		svc.EXPECT().Logout(mock.Anything, mock.Anything).Return(service.ErrInvalidToken)

		_, err := h.Logout(context.Background(), &authv1.LogoutRequest{})
		require.Equal(t, codes.Unauthenticated, status.Code(err))
	})

	t.Run("internal failure maps to Internal", func(t *testing.T) {
		svc := mocks.NewMockAuthService(t)
		h := handler.NewAuthGRPCHandler(svc)

		svc.EXPECT().Logout(mock.Anything, mock.Anything).Return(errors.New("db down"))

		_, err := h.Logout(context.Background(), &authv1.LogoutRequest{})
		require.Equal(t, codes.Internal, status.Code(err))
	})
}

func TestAuthGRPCHandler_RevokeAllSessions(t *testing.T) {
	const userID = "3f2b8c1e-9d4a-4e6b-8f7c-2a1d0e9b8c7d"

	t.Run("success", func(t *testing.T) {
		svc := mocks.NewMockAuthService(t)
		h := handler.NewAuthGRPCHandler(svc)

		svc.EXPECT().RevokeAllSessions(mock.Anything, userID).Return(2, nil)

		resp, err := h.RevokeAllSessions(context.Background(), &authv1.RevokeAllSessionsRequest{UserId: userID})
		require.NoError(t, err)
		require.EqualValues(t, 2, resp.GetRevokedCount())
	})

	t.Run("malformed user id maps to InvalidArgument", func(t *testing.T) {
		svc := mocks.NewMockAuthService(t)
		h := handler.NewAuthGRPCHandler(svc)

		svc.EXPECT().RevokeAllSessions(mock.Anything, mock.Anything).
			Return(0, fmt.Errorf("revoke sessions: %w", validator.ErrInvalidUserID))

		_, err := h.RevokeAllSessions(context.Background(), &authv1.RevokeAllSessionsRequest{UserId: "user-1"})
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("internal failure maps to Internal", func(t *testing.T) {
		svc := mocks.NewMockAuthService(t)
		h := handler.NewAuthGRPCHandler(svc)

		svc.EXPECT().RevokeAllSessions(mock.Anything, mock.Anything).Return(0, errors.New("db down"))

		_, err := h.RevokeAllSessions(context.Background(), &authv1.RevokeAllSessionsRequest{UserId: userID})
		require.Equal(t, codes.Internal, status.Code(err))
	})
}
