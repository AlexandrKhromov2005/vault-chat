package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/jwt"
)

// ErrPushUnavailable is returned when push token storage is not configured
// or a concurrent registration took the same token.
var ErrPushUnavailable = errors.New("service: push tokens unavailable")

// PushDeviceRepository stores one provider token per login (docs/push.md).
type PushDeviceRepository interface {
	RegisterPushToken(ctx context.Context, userID, sessionID, provider, token string) error
	UnregisterPushToken(ctx context.Context, userID, sessionID string) error
}

// WithPushDevices enables push token registration.
func WithPushDevices(repo PushDeviceRepository) Option {
	return func(s *Service) { s.pushDevices = repo }
}

// RegisterPushToken binds token to the login of accessToken. The token is
// checked before the input, so callers learn nothing without a valid one.
func (s *Service) RegisterPushToken(ctx context.Context, accessToken, provider, token string) error {
	claims, err := s.parseToken(ctx, accessToken, jwt.AccessToken)
	if err != nil {
		return err
	}
	if err := validator.ValidatePushToken(provider, token); err != nil {
		return err
	}
	if s.pushDevices == nil {
		return ErrPushUnavailable
	}
	err = s.pushDevices.RegisterPushToken(ctx, claims.UserID, claims.SessionID, provider, token)
	switch {
	case errors.Is(err, repository.ErrSessionRevoked):
		return ErrInvalidToken
	case errors.Is(err, repository.ErrPushTokenConflict):
		return ErrPushUnavailable
	case err != nil:
		return fmt.Errorf("register push token: %w", err)
	}
	s.logger.InfoContext(ctx, "push token registered", "user_id", claims.UserID)
	return nil
}

// UnregisterPushToken removes the token of the login of accessToken, if any.
func (s *Service) UnregisterPushToken(ctx context.Context, accessToken string) error {
	claims, err := s.parseToken(ctx, accessToken, jwt.AccessToken)
	if err != nil {
		return err
	}
	if s.pushDevices == nil {
		return ErrPushUnavailable
	}
	if err := s.pushDevices.UnregisterPushToken(ctx, claims.UserID, claims.SessionID); err != nil {
		return fmt.Errorf("unregister push token: %w", err)
	}
	return nil
}
