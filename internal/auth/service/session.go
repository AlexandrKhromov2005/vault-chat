package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/jwt"
)

// DefaultRefreshRetryWindow is how long a rotated refresh token can still be
// retried. It covers a refresh response lost on a mobile network and a client
// retrying on timeout, and is short so that it barely helps a token thief.
const DefaultRefreshRetryWindow = 30 * time.Second

// WithRefreshRetryWindow sets how long a rotated refresh token can be retried
// (see RefreshToken). A window of zero disables retries.
func WithRefreshRetryWindow(window time.Duration) Option {
	return func(s *Service) { s.refreshRetryWindow = window }
}

// RefreshToken exchanges a refresh token for a new token pair. The session of
// the presented token is revoked and replaced (rotation). A client that lost
// the response may retry with the same token for the retry window and gets a
// pair for the session the token was rotated into, as long as that session
// has not been used to refresh in turn. Any other use of a rotated token
// indicates that the token leaked, so every session of its owner is revoked.
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

	active, err := s.sessions.Rotate(ctx, claims.ID, next, s.refreshRetryWindow)
	switch {
	case err == nil && active.ID == next.ID:
		s.logger.InfoContext(ctx, "session refreshed", "user_id", user.ID)
		return pair, nil
	case err == nil:
		return s.reissueTokenPair(ctx, user, active)
	case errors.Is(err, repository.ErrNotFound), errors.Is(err, repository.ErrSessionRevoked):
		return nil, ErrInvalidToken
	case errors.Is(err, repository.ErrSessionReused):
		return nil, s.handleTokenReuse(ctx, user.ID)
	default:
		return nil, fmt.Errorf("refresh: failed to rotate session: %w", err)
	}
}

// reissueTokenPair answers a retried refresh with a new pair for the session
// that the retried token was already rotated into. No session is created, so
// the login stays a single chain of sessions.
func (s *Service) reissueTokenPair(ctx context.Context, user *domain.User, session *domain.Session) (*TokenPair, error) {
	pair, err := s.issueTokens(user, session.ID)
	if err != nil {
		return nil, fmt.Errorf("refresh: %w", err)
	}
	// The session, not the token, bounds the lifetime of the login.
	if session.ExpiresAt.Before(pair.RefreshExpiresAt) {
		pair.RefreshExpiresAt = session.ExpiresAt
	}
	s.logger.InfoContext(ctx, "session refresh retried", "user_id", user.ID)
	return pair, nil
}

// handleTokenReuse revokes every session of userID after an already rotated
// refresh token was presented. It returns ErrInvalidToken once the sessions are
// revoked, or an internal error if they could not be.
func (s *Service) handleTokenReuse(ctx context.Context, userID string) error {
	revoked, err := s.sessions.RevokeAllForUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("refresh: failed to revoke sessions after token reuse: %w", err)
	}
	s.logger.WarnContext(ctx, "rotated refresh token reused; all sessions revoked",
		"user_id", userID, "revoked_sessions", revoked)
	s.notifyRevocation(ctx, userID)
	return ErrInvalidToken
}

// Logout revokes the session bound to refreshToken together with the sessions
// it was rotated into, ending the login. Logging out of an already revoked
// session succeeds.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	claims, err := s.parseToken(ctx, refreshToken, jwt.RefreshToken)
	if err != nil {
		return err
	}

	if err := s.sessions.RevokeFamily(ctx, claims.UserID, claims.ID); err != nil {
		return fmt.Errorf("logout: failed to revoke session: %w", err)
	}

	s.logger.InfoContext(ctx, "user logged out", "user_id", claims.UserID)
	s.notifyRevocation(ctx, claims.UserID)
	return nil
}

// RevokeAllSessions revokes every active session of the owner of accessToken
// and returns how many were revoked. The token authorizes the request, so only
// its owner's sessions can be revoked.
func (s *Service) RevokeAllSessions(ctx context.Context, accessToken string) (int64, error) {
	claims, err := s.parseToken(ctx, accessToken, jwt.AccessToken)
	if err != nil {
		return 0, err
	}

	revoked, err := s.sessions.RevokeAllForUser(ctx, claims.UserID)
	if err != nil {
		return 0, fmt.Errorf("revoke sessions: %w", err)
	}

	s.logger.InfoContext(ctx, "all sessions revoked", "user_id", claims.UserID, "revoked_sessions", revoked)
	s.notifyRevocation(ctx, claims.UserID)
	return revoked, nil
}
