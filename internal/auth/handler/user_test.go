package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/handler/mocks"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
)

func TestGetUser_ReturnsOnlyPublicProfile(t *testing.T) {
	ctx := context.Background()
	svc := mocks.NewMockAuthService(t)
	svc.EXPECT().GetUser(ctx, "access", "id").Return(&domain.User{ID: "id", Username: "bob", Email: "private@example.com", PasswordHash: "secret-hash"}, nil).Once()
	resp, err := handler.NewAuthGRPCHandler(svc).GetUser(ctx, &authv1.GetUserRequest{AccessToken: "access", UserId: "id"})
	require.NoError(t, err)
	require.Equal(t, "id", resp.GetUserId())
	require.Equal(t, "bob", resp.GetUsername())
	raw, err := json.Marshal(resp)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private@example.com")
	require.NotContains(t, string(raw), "secret-hash")
}

func TestGetUser_ErrorCodesAndOpaqueFailures(t *testing.T) {
	cases := []struct {
		err  error
		code codes.Code
	}{
		{service.ErrUserNotFound, codes.NotFound}, {service.ErrInvalidToken, codes.Unauthenticated},
		{validator.ErrInvalidUserID, codes.InvalidArgument}, {context.DeadlineExceeded, codes.DeadlineExceeded},
		{context.Canceled, codes.Canceled}, {errors.New("secret database failure"), codes.Internal},
	}
	for _, tc := range cases {
		t.Run(tc.code.String(), func(t *testing.T) {
			svc := mocks.NewMockAuthService(t)
			svc.EXPECT().GetUser(context.Background(), "", "").Return(nil, tc.err).Once()
			_, err := handler.NewAuthGRPCHandler(svc).GetUser(context.Background(), nil)
			require.Equal(t, tc.code, status.Code(err))
			require.NotContains(t, status.Convert(err).Message(), "secret")
		})
	}
}
