package handler

import (
	"context"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
)

const emailAccepted = "If the request is eligible, an email will be sent."

// GetAccount returns only the account authorized by the supplied access token.
func (h *AuthGRPCHandler) GetAccount(ctx context.Context, req *authv1.GetAccountRequest) (*authv1.GetAccountResponse, error) {
	account, err := h.svc.GetAccount(ctx, req.GetAccessToken())
	if err != nil {
		return nil, toStatus(err)
	}
	if account == nil || account.UserID == "" {
		return nil, status.Error(codes.Internal, "internal error")
	}
	resp := &authv1.GetAccountResponse{
		UserId: account.UserID, Email: account.Email, Username: account.Username,
		EmailVerified: account.EmailVerified, VerificationRequired: account.VerificationRequired,
	}
	if account.VerificationDeadline != nil {
		resp.VerificationDeadline = timestamppb.New(*account.VerificationDeadline)
	}
	return resp, nil
}

// RequestEmailVerification returns the same acknowledgement for every accepted request.
func (h *AuthGRPCHandler) RequestEmailVerification(ctx context.Context, req *authv1.RequestEmailVerificationRequest) (*authv1.RequestEmailVerificationResponse, error) {
	if err := h.svc.RequestEmailVerification(ctx, req.GetEmail(), req.GetPassword()); err != nil {
		return nil, toStatus(err)
	}
	return &authv1.RequestEmailVerificationResponse{Message: emailAccepted}, nil
}

// ConfirmEmailVerification consumes an ownership proof without issuing login tokens.
func (h *AuthGRPCHandler) ConfirmEmailVerification(ctx context.Context, req *authv1.ConfirmEmailVerificationRequest) (*authv1.ConfirmEmailVerificationResponse, error) {
	owner, err := h.svc.ConfirmEmailVerification(ctx, req.GetToken())
	if err != nil {
		return nil, toStatus(err)
	}
	if owner == "" {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &authv1.ConfirmEmailVerificationResponse{Message: "Email verified.", UserId: owner}, nil
}

// RequestPasswordReset accepts recovery without disclosing account existence.
func (h *AuthGRPCHandler) RequestPasswordReset(ctx context.Context, req *authv1.RequestPasswordResetRequest) (*authv1.RequestPasswordResetResponse, error) {
	if err := h.svc.RequestPasswordReset(ctx, req.GetEmail()); err != nil {
		return nil, toStatus(err)
	}
	return &authv1.RequestPasswordResetResponse{Message: emailAccepted}, nil
}

// ConfirmPasswordReset changes a password and returns the revoked session owner.
func (h *AuthGRPCHandler) ConfirmPasswordReset(ctx context.Context, req *authv1.ConfirmPasswordResetRequest) (*authv1.ConfirmPasswordResetResponse, error) {
	owner, err := h.svc.ConfirmPasswordReset(ctx, req.GetToken(), req.GetPassword())
	if err != nil {
		return nil, toStatus(err)
	}
	if owner == "" {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &authv1.ConfirmPasswordResetResponse{Message: "Password reset.", UserId: owner}, nil
}

// RequestEmailChange requires the caller's access token and a fresh password.
func (h *AuthGRPCHandler) RequestEmailChange(ctx context.Context, req *authv1.RequestEmailChangeRequest) (*authv1.RequestEmailChangeResponse, error) {
	if err := h.svc.RequestEmailChange(ctx, req.GetAccessToken(), req.GetPassword(), req.GetNewEmail()); err != nil {
		return nil, toStatus(err)
	}
	return &authv1.RequestEmailChangeResponse{Message: emailAccepted}, nil
}

// ConfirmEmailChange consumes a new-address proof without creating a session.
func (h *AuthGRPCHandler) ConfirmEmailChange(ctx context.Context, req *authv1.ConfirmEmailChangeRequest) (*authv1.ConfirmEmailChangeResponse, error) {
	owner, err := h.svc.ConfirmEmailChange(ctx, req.GetToken())
	if err != nil {
		return nil, toStatus(err)
	}
	if owner == "" {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &authv1.ConfirmEmailChangeResponse{Message: "Email changed.", UserId: owner}, nil
}

func emailStatus(code codes.Code, message, reason string) error {
	s, err := status.New(code, message).WithDetails(&errdetails.ErrorInfo{Domain: "auth.v1", Reason: reason})
	if err != nil {
		return status.Error(codes.Internal, "internal error")
	}
	return s.Err()
}
