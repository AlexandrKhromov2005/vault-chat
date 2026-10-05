package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/jwt"
)

// RefreshToken exchanges a refresh token for a new token pair. The session of
// the presented token is revoked and replaced (rotation). Presenting a token
// whose session was already revoked indicates that the token leaked, so every
// session of its owner is revoked.
func (s *Service) RefreshToken(ctx context.Context, refreshToken string) (*TokenPair, error) {
	claims, err := s.parseToken(ctx, refreshToken, jwt.RefreshToken)
	if err != nil {
		return nil, err
	}

	// Reload the user so that new tokens carry the current identity and
	// deleted accounts cannot refresh.
	user, err := s.repo.GetByID(ctx, claims.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, fmt.Errorf("refresh: failed to load user: %w", err)
	}

	pair, next, err := s.issueTokenPair(user)
	if err != nil {
		return nil, fmt.Errorf("refresh: %w", err)
	}

	err = s.sessions.Rotate(ctx, claims.ID, next)
	switch {
	case err == nil:
		s.logger.InfoContext(ctx, "session refreshed", "user_id", user.ID)
		return pair, nil
	case errors.Is(err, repository.ErrNotFound):
		return nil, ErrInvalidToken
	case errors.Is(err, repository.ErrSessionRevoked):
		return nil, s.handleTokenReuse(ctx, user.ID)
	default:
		return nil, fmt.Errorf("refresh: failed to rotate session: %w", err)
	}
}

// handleTokenReuse revokes every session of userID after a revoked refresh
// token was presented. It returns ErrInvalidToken once the sessions are
// revoked, or an internal error if they could not be.
func (s *Service) handleTokenReuse(ctx context.Context, userID string) error {
	revoked, err := s.sessions.RevokeAllForUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("refresh: failed to revoke sessions after token reuse: %w", err)
	}
	s.logger.WarnContext(ctx, "revoked refresh token reused; all sessions revoked",
		"user_id", userID, "revoked_sessions", revoked)
	return ErrInvalidToken
}

// Logout revokes the session bound to refreshToken. Logging out of an
// already revoked session succeeds.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	claims, err := s.parseToken(ctx, refreshToken, jwt.RefreshToken)
	if err != nil {
		return err
	}

	if err := s.sessions.Revoke(ctx, claims.ID); err != nil {
		return fmt.Errorf("logout: failed to revoke session: %w", err)
	}

	s.logger.InfoContext(ctx, "user logged out", "user_id", claims.UserID)
	return nil
}

// RevokeAllSessions revokes every active session of userID and returns how
// many were revoked. Authorizing the caller is the gateway's responsibility.
func (s *Service) RevokeAllSessions(ctx context.Context, userID string) (int64, error) {
	if err := validator.ValidateUserID(userID); err != nil {
		return 0, fmt.Errorf("revoke sessions: %w", err)
	}

	revoked, err := s.sessions.RevokeAllForUser(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("revoke sessions: %w", err)
	}

	s.logger.InfoContext(ctx, "all sessions revoked", "user_id", userID, "revoked_sessions", revoked)
	return revoked, nil
}
