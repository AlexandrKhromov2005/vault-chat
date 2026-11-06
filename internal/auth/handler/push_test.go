package handler_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/handler/mocks"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
)

func TestAuthGRPCHandler_RegisterPushToken(t *testing.T) {
	req := &authv1.RegisterPushTokenRequest{AccessToken: "access-token", Provider: "fcm", Token: "device-token"}
	for name, tc := range map[string]struct {
		err     error
		code    codes.Code
		message string
	}{
		"success":         {nil, codes.OK, ""},
		"invalid token":   {fmt.Errorf("wrapped: %w", service.ErrInvalidToken), codes.Unauthenticated, "invalid token"},
		"malformed input": {validator.ErrInvalidPushToken, codes.InvalidArgument, "invalid push token"},
		"unavailable":     {service.ErrPushUnavailable, codes.Unavailable, "push tokens unavailable"},
		"internal":        {errors.New("db down: device-token"), codes.Internal, "internal error"},
	} {
		t.Run(name, func(t *testing.T) {
			svc := mocks.NewMockAuthService(t)
			svc.EXPECT().RegisterPushToken(mock.Anything, "access-token", "fcm", "device-token").Return(tc.err)
			_, err := handler.NewAuthGRPCHandler(svc).RegisterPushToken(context.Background(), req)
			require.Equal(t, tc.code, status.Code(err))
			if tc.err != nil {
				require.Equal(t, tc.message, status.Convert(err).Message(), "only the fixed public message is sent")
			}
		})
	}
}

func TestAuthGRPCHandler_UnregisterPushToken(t *testing.T) {
	svc := mocks.NewMockAuthService(t)
	svc.EXPECT().UnregisterPushToken(mock.Anything, "access-token").Return(nil).Once()
	h := handler.NewAuthGRPCHandler(svc)
	_, err := h.UnregisterPushToken(context.Background(), &authv1.UnregisterPushTokenRequest{AccessToken: "access-token"})
	require.NoError(t, err)

	svc.EXPECT().UnregisterPushToken(mock.Anything, "").Return(service.ErrInvalidToken).Once()
	_, err = h.UnregisterPushToken(context.Background(), &authv1.UnregisterPushTokenRequest{})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}
