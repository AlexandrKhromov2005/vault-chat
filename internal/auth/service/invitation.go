package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
)

// ErrInvalidInvitation deliberately reveals no email/account availability.
var ErrInvalidInvitation = errors.New("invalid or expired invitation")

// InvitationRepository consumes email-bound invitations with user creation.
type InvitationRepository interface {
	InvitationValid(context.Context, []byte, string) (bool, error)
	CreateInvited(context.Context, *domain.User, []byte) error
}

// Option configures optional Auth collaborators.
type Option func(*Service)

// WithInvitations enables invitation-only public registration.
func WithInvitations(repo InvitationRepository) Option {
	return func(s *Service) { s.invitations = repo }
}

// RegisterInvited is the only registration operation exposed over RPC. An
// absent invitation store fails closed. Register itself is for local operators.
func (s *Service) RegisterInvited(ctx context.Context, email, username, password, token string) (*domain.User, error) {
	if err := validator.ValidateEmail(email); err != nil {
		return nil, err
	}
	if len(token) != 64 || s.invitations == nil {
		return nil, ErrInvalidInvitation
	}
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) != 32 || s.invitations == nil {
		return nil, ErrInvalidInvitation
	}
	hash := sha256.Sum256([]byte(token))
	valid, err := s.invitations.InvitationValid(ctx, hash[:], email)
	if err != nil {
		return nil, fmt.Errorf("check invitation: %w", err)
	}
	if !valid {
		return nil, ErrInvalidInvitation
	}
	user, err := s.register(ctx, email, username, password, func(ctx context.Context, user *domain.User) error {
		return s.invitations.CreateInvited(ctx, user, hash[:])
	})
	if errors.Is(err, ErrEmailTaken) || errors.Is(err, repository.ErrInvalidInvitation) {
		return nil, ErrInvalidInvitation
	}
	return user, err
}
