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

// SearchUsers lists identities whose username starts with query, ignoring
// case, for any authenticated access-token holder. The caller is excluded
// because a dialog with oneself is not allowed. As with GetUser, the transport
// must project only public profile fields.
func (s *Service) SearchUsers(ctx context.Context, accessToken, query string, limit int32) ([]*domain.User, error) {
	claims, err := s.parseToken(ctx, accessToken, jwt.AccessToken)
	if err != nil {
		return nil, err
	}
	if err := validator.ValidateUsernameQuery(query); err != nil {
		return nil, err
	}
	size, err := validator.SearchLimit(limit)
	if err != nil {
		return nil, err
	}
	users, err := s.repo.SearchByUsernamePrefix(ctx, query, claims.UserID, size)
	if err != nil {
		return nil, fmt.Errorf("search users: %w", err)
	}
	return users, nil
}
