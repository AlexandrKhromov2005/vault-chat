package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/jwt"
)

func TestRevokedSessionRejectsAnUnexpiredAccessToken(t *testing.T) {
	fx := newServiceFixture(t)
	const sid = "b7f0d975-5345-4b94-a4b8-7ecc70562e22"
	fx.tokens.EXPECT().Validate("access").Return(&jwt.Claims{UserID: "owner", SessionID: sid, TokenType: jwt.AccessToken}, nil)
	fx.sessions.EXPECT().ActiveFamily(mock.Anything, "owner", sid).Return("", nil)
	_, err := fx.svc.ValidateToken(context.Background(), "access")
	require.ErrorIs(t, err, service.ErrInvalidToken)
}

func TestValidateSessionChecksCurrentFamily(t *testing.T) {
	fx := newServiceFixture(t)
	const sid = "b7f0d975-5345-4b94-a4b8-7ecc70562e22"
	const user = "11111111-1111-4111-8111-111111111111"
	fx.sessions.EXPECT().ActiveFamily(mock.Anything, user, sid).Return(sid, nil).Once()
	active, err := fx.svc.ValidateSession(context.Background(), user, sid)
	require.NoError(t, err)
	require.True(t, active)

	fx.sessions.EXPECT().ActiveFamily(mock.Anything, user, sid).Return("", nil)
	active, err = fx.svc.ValidateSession(context.Background(), user, sid)
	require.NoError(t, err)
	require.False(t, active)

	active, err = fx.svc.ValidateSession(context.Background(), "not-a-user", sid)
	require.NoError(t, err)
	require.False(t, active)
}

func TestLegacyAccessTokenWithoutSessionFailsClosed(t *testing.T) {
	fx := newServiceFixture(t)
	fx.tokens.EXPECT().Validate("legacy").Return(&jwt.Claims{UserID: "owner", TokenType: jwt.AccessToken}, nil)
	_, err := fx.svc.ValidateToken(context.Background(), "legacy")
	require.ErrorIs(t, err, service.ErrInvalidToken)
}
