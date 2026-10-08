// Package handler exposes the auth service over gRPC.
package handler

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	Register(ctx context.Context, email, username, password string) (*domain.User, error)
	Login(ctx context.Context, email, password string) (*service.TokenPair, error)
	ValidateToken(ctx context.Context, token string) (*jwt.Claims, error)
	RefreshToken(ctx context.Context, refreshToken string) (*service.TokenPair, error)
	Logout(ctx context.Context, refreshToken string) error
	RevokeAllSessions(ctx context.Context, accessToken string) (int64, error)
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
	user, err := h.svc.Register(ctx, req.GetEmail(), req.GetUsername(), req.GetPassword())
	if err != nil {
		return nil, toStatus(err)
	}
	return &authv1.RegisterResponse{
		UserId:   user.ID,
		Email:    user.Email,
		Username: user.Username,
	}, nil
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
	for _, e := range publicErrors {
		if errors.Is(err, e.err) {
			return status.Error(e.code, e.message)
		}
	}
	return status.Error(codes.Internal, "internal error")
}
