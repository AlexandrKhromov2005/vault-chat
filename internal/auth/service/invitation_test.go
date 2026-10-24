package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
)

type invitations struct {
	valid     bool
	createErr error
	creates   int
}

func (i *invitations) InvitationValid(context.Context, []byte, string) (bool, error) {
	return i.valid, nil
}
func (i *invitations) CreateInvited(context.Context, *domain.User, []byte) error {
	i.creates++
	return i.createErr
}

func TestPublicRegistrationRequiresInvitation(t *testing.T) {
	fx := newServiceFixture(t)
	for _, email := range []string{"existing@example.com", "unknown@example.com"} {
		for _, token := range []string{"", "invalid", strings.Repeat("a", 64)} {
			_, err := fx.svc.RegisterInvited(context.Background(), email, "new_user", "password1", token)
			require.ErrorIs(t, err, service.ErrInvalidInvitation)
		}
	}
}

func TestInvitedRegistrationDoesNotRevealExistingEmail(t *testing.T) {
	fx := newServiceFixture(t)
	store := &invitations{valid: true, createErr: repository.ErrEmailTaken}
	service.WithInvitations(store)(fx.svc)
	_, err := fx.svc.RegisterInvited(context.Background(), "existing@example.com", "new_user", "password1", strings.Repeat("a", 64))
	require.ErrorIs(t, err, service.ErrInvalidInvitation)
	require.Equal(t, 1, store.creates)
	store.createErr = repository.ErrInvalidInvitation
	_, err = fx.svc.RegisterInvited(context.Background(), "existing@example.com", "new_user", "password1", strings.Repeat("a", 64))
	require.ErrorIs(t, err, service.ErrInvalidInvitation)
}
