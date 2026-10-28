// Package handler exposes the auth service over gRPC.
package handler

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/jwt"
)

// AuthService is the business-logic contract required by the handler.
// Defined at the consumer per CONTRIBUTING.md.
type AuthService interface {
	GetUser(ctx context.Context, accessToken, userID string) (*domain.User, error)
	SearchUsers(ctx context.Context, accessToken, query string, limit int32) ([]*domain.User, error)
	RegisterInvited(ctx context.Context, email, username, password, invitationToken string) (*domain.User, error)
	Login(ctx context.Context, email, password string) (*service.TokenPair, error)
	ValidateToken(ctx context.Context, token string) (*jwt.Claims, error)
	RefreshToken(ctx context.Context, refreshToken string) (*service.TokenPair, error)
	Logout(ctx context.Context, refreshToken string) error
	RevokeAllSessions(ctx context.Context, accessToken string) (int64, error)
	GetAccount(ctx context.Context, accessToken string) (*service.AccountInfo, error)
	RequestEmailVerification(ctx context.Context, email, password string) error
	ConfirmEmailVerification(ctx context.Context, token string) (string, error)
	RequestPasswordReset(ctx context.Context, email string) error
	ConfirmPasswordReset(ctx context.Context, token, password string) (string, error)
	RequestEmailChange(ctx context.Context, accessToken, password, newEmail string) error
	ConfirmEmailChange(ctx context.Context, token string) (string, error)
	RegisterPushToken(ctx context.Context, accessToken, provider, token string) error
	UnregisterPushToken(ctx context.Context, accessToken string) error
}

// AuthGRPCHandler implements authv1.AuthServiceServer.
type AuthGRPCHandler struct {
	authv1.UnimplementedAuthServiceServer
	svc AuthService
}

// NewAuthGRPCHandler creates a handler backed by the given service.
func NewAuthGRPCHandler(svc AuthService) *AuthGRPCHandler {
	return &AuthGRPCHandler{svc: svc}
}

// Register creates a new user account.
func (h *AuthGRPCHandler) Register(ctx context.Context, req *authv1.RegisterRequest) (*authv1.RegisterResponse, error) {
	user, err := h.svc.RegisterInvited(ctx, req.GetEmail(), req.GetUsername(), req.GetPassword(), req.GetInvitationToken())
	if err != nil {
		return nil, toStatus(err)
	}
	resp := &authv1.RegisterResponse{
		UserId:   user.ID,
		Email:    user.Email,
		Username: user.Username,
	}
	if user.EmailVerificationRequired && user.EmailVerifiedAt == nil {
		resp.VerificationRequired = proto.Bool(true)
	}
	return resp, nil
}

// Login authenticates a user and issues an access/refresh token pair.
func (h *AuthGRPCHandler) Login(ctx context.Context, req *authv1.LoginRequest) (*authv1.LoginResponse, error) {
	pair, err := h.svc.Login(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		return nil, toStatus(err)
	}
	return &authv1.LoginResponse{
		UserId:           pair.UserID,
		AccessToken:      pair.AccessToken,
		RefreshToken:     pair.RefreshToken,
		AccessExpiresAt:  timestamppb.New(pair.AccessExpiresAt),
		RefreshExpiresAt: timestamppb.New(pair.RefreshExpiresAt),
	}, nil
}

// ValidateToken verifies a JWT. A syntactically well-formed but invalid token
// yields valid=false rather than a status error, so callers (the gateway) can
// distinguish rejection from failure.
func (h *AuthGRPCHandler) ValidateToken(ctx context.Context, req *authv1.ValidateTokenRequest) (*authv1.ValidateTokenResponse, error) {
	claims, err := h.svc.ValidateToken(ctx, req.GetToken())
	if errors.Is(err, service.ErrInvalidToken) {
		return &authv1.ValidateTokenResponse{Valid: false}, nil
	}
	if err != nil {
		return nil, toStatus(err)
	}
	return &authv1.ValidateTokenResponse{
		Valid:     true,
		UserId:    claims.UserID,
		Email:     claims.Email,
		Username:  claims.Username,
		ExpiresAt: timestamppb.New(claims.ExpiresAt),
	}, nil
}

// RefreshToken exchanges a refresh token for a new access/refresh token pair.
func (h *AuthGRPCHandler) RefreshToken(ctx context.Context, req *authv1.RefreshTokenRequest) (*authv1.RefreshTokenResponse, error) {
	pair, err := h.svc.RefreshToken(ctx, req.GetRefreshToken())
	if err != nil {
		return nil, toStatus(err)
	}
	return &authv1.RefreshTokenResponse{
		UserId:           pair.UserID,
		AccessToken:      pair.AccessToken,
		RefreshToken:     pair.RefreshToken,
		AccessExpiresAt:  timestamppb.New(pair.AccessExpiresAt),
		RefreshExpiresAt: timestamppb.New(pair.RefreshExpiresAt),
	}, nil
}

// Logout revokes the session bound to the given refresh token.
func (h *AuthGRPCHandler) Logout(ctx context.Context, req *authv1.LogoutRequest) (*authv1.LogoutResponse, error) {
	if err := h.svc.Logout(ctx, req.GetRefreshToken()); err != nil {
		return nil, toStatus(err)
	}
	return &authv1.LogoutResponse{}, nil
}

// RevokeAllSessions revokes every active session of the owner of the given
// access token.
func (h *AuthGRPCHandler) RevokeAllSessions(
	ctx context.Context,
	req *authv1.RevokeAllSessionsRequest,
) (*authv1.RevokeAllSessionsResponse, error) {
	revoked, err := h.svc.RevokeAllSessions(ctx, req.GetAccessToken())
	if err != nil {
		return nil, toStatus(err)
	}
	return &authv1.RevokeAllSessionsResponse{RevokedCount: revoked}, nil
}

// publicErrors are the domain errors reported to clients, with their gRPC
// codes and messages. Any other error is reported as an opaque Internal error.
var publicErrors = []struct {
	err     error
	code    codes.Code
	message string
}{
	{service.ErrInvalidInvitation, codes.PermissionDenied, "invalid or expired invitation"},
	{service.ErrPasswordCapacity, codes.Unavailable, "password capacity reached"},
	{service.ErrEmailUnavailable, codes.Unavailable, "email service unavailable"},
	{service.ErrLoginRateLimited, codes.ResourceExhausted, "too many login attempts"},
	{service.ErrPushUnavailable, codes.Unavailable, "push tokens unavailable"},
	{validator.ErrInvalidPushToken, codes.InvalidArgument, "invalid push token"},
	{context.DeadlineExceeded, codes.DeadlineExceeded, "request timed out"},
	{context.Canceled, codes.Canceled, "request canceled"},
	{validator.ErrInvalidUserID, codes.InvalidArgument, "invalid user identifier"},
	{validator.ErrInvalidUsernameQuery, codes.InvalidArgument, "invalid username query"},
	{validator.ErrInvalidSearchLimit, codes.InvalidArgument, "invalid search limit"},
	{service.ErrUserNotFound, codes.NotFound, "user not found"},
	{validator.ErrInvalidEmail, codes.InvalidArgument, "invalid email address"},
	{validator.ErrInvalidUsername, codes.InvalidArgument, "invalid username"},
	{validator.ErrInvalidPassword, codes.InvalidArgument, "invalid password"},
	{service.ErrEmailTaken, codes.AlreadyExists, "email already registered"},
	{service.ErrUsernameTaken, codes.AlreadyExists, "username already taken"},
	{service.ErrInvalidCredentials, codes.Unauthenticated, "invalid credentials"},
	{service.ErrInvalidToken, codes.Unauthenticated, "invalid token"},
}

// toStatus maps domain errors to gRPC status codes. Only the fixed public
// message is sent, never the wrapping context of err.
func toStatus(err error) error {
	if errors.Is(err, service.ErrEmailUnverified) {
		return emailStatus(codes.FailedPrecondition, "email verification required", "EMAIL_VERIFICATION_REQUIRED")
	}
	if errors.Is(err, service.ErrInvalidEmailAction) {
		return emailStatus(codes.InvalidArgument, "invalid email action", "INVALID_EMAIL_ACTION")
	}
	for _, e := range publicErrors {
		if errors.Is(err, e.err) {
			return status.Error(e.code, e.message)
		}
	}
	return status.Error(codes.Internal, "internal error")
}

// RegisterPushToken binds a provider token to the caller's login.
func (h *AuthGRPCHandler) RegisterPushToken(ctx context.Context, req *authv1.RegisterPushTokenRequest) (*authv1.RegisterPushTokenResponse, error) {
	if err := h.svc.RegisterPushToken(ctx, req.GetAccessToken(), req.GetProvider(), req.GetToken()); err != nil {
		return nil, toStatus(err)
	}
	return &authv1.RegisterPushTokenResponse{}, nil
}

// UnregisterPushToken removes the token of the caller's login.
func (h *AuthGRPCHandler) UnregisterPushToken(ctx context.Context, req *authv1.UnregisterPushTokenRequest) (*authv1.UnregisterPushTokenResponse, error) {
	if err := h.svc.UnregisterPushToken(ctx, req.GetAccessToken()); err != nil {
		return nil, toStatus(err)
	}
	return &authv1.UnregisterPushTokenResponse{}, nil
}

// GetUser resolves a user and projects only the public directory fields.
func (h *AuthGRPCHandler) GetUser(ctx context.Context, req *authv1.GetUserRequest) (*authv1.GetUserResponse, error) {
	user, err := h.svc.GetUser(ctx, req.GetAccessToken(), req.GetUserId())
	if err != nil {
		return nil, toStatus(err)
	}
	return &authv1.GetUserResponse{UserId: user.ID, Username: user.Username}, nil
}

// SearchUsers finds users by username prefix and projects only the public
// directory fields.
func (h *AuthGRPCHandler) SearchUsers(ctx context.Context, req *authv1.SearchUsersRequest) (*authv1.SearchUsersResponse, error) {
	users, err := h.svc.SearchUsers(ctx, req.GetAccessToken(), req.GetQuery(), req.GetLimit())
	if err != nil {
		return nil, toStatus(err)
	}
	resp := &authv1.SearchUsersResponse{Users: make([]*authv1.UserProfile, 0, len(users))}
	for _, user := range users {
		resp.Users = append(resp.Users, &authv1.UserProfile{UserId: user.ID, Username: user.Username})
	}
	return resp, nil
}
