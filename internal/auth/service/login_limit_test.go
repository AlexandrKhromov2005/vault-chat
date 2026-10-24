package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
)

func TestLoginQuotaUsesCaseInsensitiveEmailForUnknownAccounts(t *testing.T) {
	fx := newServiceFixture(t)
	fx.repo.EXPECT().GetByEmail(mock.Anything, mock.Anything).
		Return(nil, repository.ErrNotFound).Times(5)
	for i := range 6 {
		email := "account@example.com"
		if i%2 == 1 {
			email = strings.ToUpper(email)
		}
		_, err := fx.svc.Login(context.Background(), email, "password1")
		if i == 5 {
			require.ErrorIs(t, err, service.ErrLoginRateLimited)
		} else {
			require.ErrorIs(t, err, service.ErrInvalidCredentials)
		}
	}
}
