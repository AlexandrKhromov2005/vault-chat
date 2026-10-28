package service_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service/mocks"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/jwt"
)

const pushSession = "11111111-1111-4111-8111-111111111111"

// fakePushDevices records calls and answers with err.
type fakePushDevices struct {
	err   error
	calls []string
}

func (f *fakePushDevices) RegisterPushToken(_ context.Context, userID, sessionID, provider, token string) error {
	f.calls = append(f.calls, "register "+userID+" "+sessionID+" "+provider+" "+token)
	return f.err
}

func (f *fakePushDevices) UnregisterPushToken(_ context.Context, userID, sessionID string) error {
	f.calls = append(f.calls, "unregister "+userID+" "+sessionID)
	return f.err
}

func newPushService(t *testing.T, devices service.PushDeviceRepository) (*service.Service, *mocks.MockTokenManager) {
	t.Helper()
	sessions := mocks.NewMockSessionRepository(t)
	sessions.EXPECT().IsActive(mock.Anything, mock.Anything, pushSession).Return(true, nil).Maybe()
	tokens := mocks.NewMockTokenManager(t)
	var options []service.Option
	if devices != nil {
		options = append(options, service.WithPushDevices(devices))
	}
	svc, err := service.NewService(mocks.NewMockUserRepository(t), sessions, service.NewArgon2idHasher(fastParams), tokens, slog.Default(), options...)
	require.NoError(t, err)
	return svc, tokens
}

// accessFor makes "access" a valid access token of pushSession.
func accessFor(tokens *mocks.MockTokenManager) {
	tokens.EXPECT().Validate("access").Return(&jwt.Claims{UserID: "user-1", SessionID: pushSession, TokenType: jwt.AccessToken}, nil)
}

func TestRegisterPushToken(t *testing.T) {
	ctx := context.Background()

	t.Run("binds the token to the caller's login", func(t *testing.T) {
		devices := &fakePushDevices{}
		svc, tokens := newPushService(t, devices)
		accessFor(tokens)
		require.NoError(t, svc.RegisterPushToken(ctx, "access", "fcm", "device-token"))
		require.Equal(t, []string{"register user-1 " + pushSession + " fcm device-token"}, devices.calls)
	})

	t.Run("checks the access token before the input", func(t *testing.T) {
		devices := &fakePushDevices{}
		svc, tokens := newPushService(t, devices)
		tokens.EXPECT().Validate("bad").Return(nil, errors.New("signature invalid"))
		require.ErrorIs(t, svc.RegisterPushToken(ctx, "bad", "apns", ""), service.ErrInvalidToken)
		require.ErrorIs(t, svc.RegisterPushToken(ctx, "", "fcm", "device-token"), service.ErrInvalidToken)
		tokens.EXPECT().Validate("refresh").Return(&jwt.Claims{UserID: "user-1", ID: pushSession, TokenType: jwt.RefreshToken}, nil)
		require.ErrorIs(t, svc.RegisterPushToken(ctx, "refresh", "fcm", "device-token"), service.ErrInvalidToken)
		require.Empty(t, devices.calls)
	})

	t.Run("rejects malformed tokens and unknown providers", func(t *testing.T) {
		devices := &fakePushDevices{}
		svc, tokens := newPushService(t, devices)
		accessFor(tokens)
		for _, tc := range []struct{ provider, token string }{{"fcm", ""}, {"fcm", "a b"}, {"apns", "device-token"}} {
			require.ErrorIs(t, svc.RegisterPushToken(ctx, "access", tc.provider, tc.token), validator.ErrInvalidPushToken)
		}
		require.Empty(t, devices.calls)
	})

	t.Run("a login revoked meanwhile is an invalid token", func(t *testing.T) {
		svc, tokens := newPushService(t, &fakePushDevices{err: repository.ErrSessionRevoked})
		accessFor(tokens)
		require.ErrorIs(t, svc.RegisterPushToken(ctx, "access", "fcm", "device-token"), service.ErrInvalidToken)
	})

	t.Run("storage failures", func(t *testing.T) {
		svc, tokens := newPushService(t, &fakePushDevices{err: repository.ErrPushTokenConflict})
		accessFor(tokens)
		require.ErrorIs(t, svc.RegisterPushToken(ctx, "access", "fcm", "device-token"), service.ErrPushUnavailable)

		down := errors.New("connection refused")
		svc, tokens = newPushService(t, &fakePushDevices{err: down})
		accessFor(tokens)
		require.ErrorIs(t, svc.RegisterPushToken(ctx, "access", "fcm", "device-token"), down)

		svc, tokens = newPushService(t, nil)
		accessFor(tokens)
		require.ErrorIs(t, svc.RegisterPushToken(ctx, "access", "fcm", "device-token"), service.ErrPushUnavailable)
	})
}

func TestUnregisterPushToken(t *testing.T) {
	ctx := context.Background()
	devices := &fakePushDevices{}
	svc, tokens := newPushService(t, devices)
	accessFor(tokens)
	require.NoError(t, svc.UnregisterPushToken(ctx, "access"))
	require.Equal(t, []string{"unregister user-1 " + pushSession}, devices.calls)

	tokens.EXPECT().Validate("bad").Return(nil, errors.New("expired"))
	require.ErrorIs(t, svc.UnregisterPushToken(ctx, "bad"), service.ErrInvalidToken)
	require.Len(t, devices.calls, 1)
}
