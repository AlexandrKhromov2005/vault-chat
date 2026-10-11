package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/jwt"
)

// ErrUserNotFound means the requested directory identity does not exist.
var ErrUserNotFound = errors.New("service: user not found")

// GetUser resolves an identity for any authenticated access-token holder.
// The transport must project only public profile fields from the returned user.
func (s *Service) GetUser(ctx context.Context, accessToken, userID string) (*domain.User, error) {
	if _, err := s.parseToken(ctx, accessToken, jwt.AccessToken); err != nil {
		return nil, err
	}
	id, err := validator.CanonicalUserID(userID)
	if err != nil {
		return nil, err
	}
	user, err := s.repo.GetByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lookup user: %w", err)
	}
	return user, nil
}
