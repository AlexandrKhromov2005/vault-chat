package handler_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/handler/mocks"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
)

func TestEmailVerificationRequiredHasNarrowPublicReason(t *testing.T) {
	svc := mocks.NewMockAuthService(t)
	svc.EXPECT().Login(mock.Anything, "alice@example.invalid", "Password12").
		Return(nil, fmt.Errorf("private context: %w", service.ErrEmailUnverified))
	_, err := handler.NewAuthGRPCHandler(svc).Login(context.Background(), &authv1.LoginRequest{
		Email: "alice@example.invalid", Password: "Password12",
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Equal(t, "email verification required", status.Convert(err).Message())
	details := status.Convert(err).Details()
	require.Len(t, details, 1)
	info, ok := details[0].(*errdetails.ErrorInfo)
	require.True(t, ok)
	require.Equal(t, "auth.v1", info.GetDomain())
	require.Equal(t, "EMAIL_VERIFICATION_REQUIRED", info.GetReason())
}

func TestAccountGRPCProjectsPrivateStateAndOptionalDeadline(t *testing.T) {
	for _, withDeadline := range []bool{false, true} {
		svc := mocks.NewMockAuthService(t)
		account := &service.AccountInfo{UserID: "owner", Email: "alice@example.invalid", Username: "alice", VerificationRequired: true}
		if withDeadline {
			deadline := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
			account.VerificationDeadline = &deadline
		}
		svc.EXPECT().GetAccount(mock.Anything, "own-access").Return(account, nil)
		got, err := handler.NewAuthGRPCHandler(svc).GetAccount(context.Background(), &authv1.GetAccountRequest{AccessToken: "own-access"})
		require.NoError(t, err)
		require.Equal(t, "owner", got.GetUserId())
		require.Equal(t, account.Email, got.GetEmail())
		require.True(t, got.GetVerificationRequired())
		require.False(t, got.GetEmailVerified())
		if withDeadline {
			require.Equal(t, *account.VerificationDeadline, got.GetVerificationDeadline().AsTime())
		} else {
			require.Nil(t, got.GetVerificationDeadline())
		}
	}
}

func TestEmailRequestGRPCForwardsCredentialsAndAcknowledgesGenerically(t *testing.T) {
	svc := mocks.NewMockAuthService(t)
	h := handler.NewAuthGRPCHandler(svc)
	svc.EXPECT().RequestEmailVerification(mock.Anything, "alice@example.invalid", "fresh-password").Return(nil)
	verify, err := h.RequestEmailVerification(context.Background(), &authv1.RequestEmailVerificationRequest{Email: "alice@example.invalid", Password: "fresh-password"})
	require.NoError(t, err)
	svc.EXPECT().RequestPasswordReset(mock.Anything, "unknown@example.invalid").Return(nil)
	reset, err := h.RequestPasswordReset(context.Background(), &authv1.RequestPasswordResetRequest{Email: "unknown@example.invalid"})
	require.NoError(t, err)
	svc.EXPECT().RequestEmailChange(mock.Anything, "own-access", "fresh-password", "new@example.invalid").Return(nil)
	change, err := h.RequestEmailChange(context.Background(), &authv1.RequestEmailChangeRequest{AccessToken: "own-access", Password: "fresh-password", NewEmail: "new@example.invalid"})
	require.NoError(t, err)
	require.Equal(t, verify.GetMessage(), reset.GetMessage())
	require.Equal(t, verify.GetMessage(), change.GetMessage())
	require.NotContains(t, verify.GetMessage(), "alice")
}

func TestEmailConfirmationGRPCRejectsBadTokensAndNeverIssuesJWT(t *testing.T) {
	type completion interface {
		GetUserId() string
		GetMessage() string
	}
	for _, tc := range []struct {
		name string
		call func(*handler.AuthGRPCHandler, string) (completion, error)
	}{
		{"ConfirmEmailVerification", func(h *handler.AuthGRPCHandler, token string) (completion, error) {
			return h.ConfirmEmailVerification(context.Background(), &authv1.ConfirmEmailVerificationRequest{Token: token})
		}},
		{"ConfirmPasswordReset", func(h *handler.AuthGRPCHandler, token string) (completion, error) {
			return h.ConfirmPasswordReset(context.Background(), &authv1.ConfirmPasswordResetRequest{Token: token, Password: "NewPassword12"})
		}},
		{"ConfirmEmailChange", func(h *handler.AuthGRPCHandler, token string) (completion, error) {
			return h.ConfirmEmailChange(context.Background(), &authv1.ConfirmEmailChangeRequest{Token: token})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, token := range []string{"synthetic-token", ""} {
				svc := mocks.NewMockAuthService(t)
				args := []any{mock.Anything, token}
				if tc.name == "ConfirmPasswordReset" {
					args = append(args, "NewPassword12")
				}
				var domainErr error
				owner := "owner"
				if token == "" {
					domainErr = fmt.Errorf("private context: %w", service.ErrInvalidEmailAction)
					owner = ""
				}
				svc.On(tc.name, args...).Return(owner, domainErr).Once()
				got, err := tc.call(handler.NewAuthGRPCHandler(svc), token)
				if token == "" {
					require.Equal(t, codes.InvalidArgument, status.Code(err))
					require.Equal(t, "invalid email action", status.Convert(err).Message())
					info := status.Convert(err).Details()[0].(*errdetails.ErrorInfo)
					require.Equal(t, "INVALID_EMAIL_ACTION", info.GetReason())
				} else {
					require.NoError(t, err)
					require.Equal(t, owner, got.GetUserId())
					require.NotContains(t, got.GetMessage(), token)
				}
			}
		})
	}
}

func TestEmailGRPCFailureMappingsRemainSafeAndDistinct(t *testing.T) {
	for _, tc := range []struct {
		err     error
		code    codes.Code
		message string
	}{
		{service.ErrEmailUnavailable, codes.Unavailable, "email service unavailable"},
		{service.ErrLoginRateLimited, codes.ResourceExhausted, "too many login attempts"},
		{service.ErrInvalidCredentials, codes.Unauthenticated, "invalid credentials"},
		{validator.ErrInvalidPassword, codes.InvalidArgument, "invalid password"},
		{fmt.Errorf("private database failure"), codes.Internal, "internal error"},
	} {
		svc := mocks.NewMockAuthService(t)
		svc.EXPECT().ConfirmPasswordReset(mock.Anything, "synthetic-token", "password").Return("", fmt.Errorf("private context: %w", tc.err))
		_, err := handler.NewAuthGRPCHandler(svc).ConfirmPasswordReset(context.Background(), &authv1.ConfirmPasswordResetRequest{Token: "synthetic-token", Password: "password"})
		require.Equal(t, tc.code, status.Code(err))
		require.Equal(t, tc.message, status.Convert(err).Message())
	}
}

func TestRegistrationReportsVerificationOnlyForRequiredUnverifiedUsers(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		required bool
		verified *time.Time
		want     bool
	}{
		{true, nil, true}, {false, nil, false}, {true, &now, false},
	} {
		svc := mocks.NewMockAuthService(t)
		svc.EXPECT().RegisterInvited(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&domain.User{ID: "owner", EmailVerificationRequired: tc.required, EmailVerifiedAt: tc.verified}, nil)
		got, err := handler.NewAuthGRPCHandler(svc).Register(context.Background(), &authv1.RegisterRequest{})
		require.NoError(t, err)
		require.Equal(t, tc.want, got.GetVerificationRequired())
		if !tc.want {
			require.Nil(t, got.VerificationRequired)
		}
	}
}
