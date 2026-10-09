// Package handler implements the REST endpoints of the API gateway by
// translating them into gRPC calls to backend services.
package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/response"
)

// AuthClient is the part of the auth gRPC client used by the REST handlers.
// Defined at the consumer per CONTRIBUTING.md; authv1.AuthServiceClient
// satisfies it.
type AuthClient interface {
	Register(ctx context.Context, in *authv1.RegisterRequest, opts ...grpc.CallOption) (*authv1.RegisterResponse, error)
	Login(ctx context.Context, in *authv1.LoginRequest, opts ...grpc.CallOption) (*authv1.LoginResponse, error)
	RefreshToken(ctx context.Context, in *authv1.RefreshTokenRequest, opts ...grpc.CallOption) (*authv1.RefreshTokenResponse, error)
	Logout(ctx context.Context, in *authv1.LogoutRequest, opts ...grpc.CallOption) (*authv1.LogoutResponse, error)
	RevokeAllSessions(ctx context.Context, in *authv1.RevokeAllSessionsRequest, opts ...grpc.CallOption) (*authv1.RevokeAllSessionsResponse, error)
}

type registerRequest struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type userResponse struct {
	UserID   string `json:"user_id"`
	Email    string `json:"email"`
	Username string `json:"username"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type tokenResponse struct {
	UserID           string    `json:"user_id"`
	TokenType        string    `json:"token_type"`
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

// tokenPair is implemented by both authv1.LoginResponse and
// authv1.RefreshTokenResponse.
type tokenPair interface {
	GetUserId() string
	GetAccessToken() string
	GetRefreshToken() string
	GetAccessExpiresAt() *timestamppb.Timestamp
	GetRefreshExpiresAt() *timestamppb.Timestamp
}

func newTokenResponse(pair tokenPair) tokenResponse {
	return tokenResponse{
		UserID:           pair.GetUserId(),
		TokenType:        "Bearer",
		AccessToken:      pair.GetAccessToken(),
		RefreshToken:     pair.GetRefreshToken(),
		AccessExpiresAt:  pair.GetAccessExpiresAt().AsTime(),
		RefreshExpiresAt: pair.GetRefreshExpiresAt().AsTime(),
	}
}

type refreshTokenRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type meResponse struct {
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	Username  string    `json:"username"`
	ExpiresAt time.Time `json:"expires_at"`
}

type revokeAllSessionsResponse struct {
	RevokedCount int64 `json:"revoked_count"`
}

// AuthHandler serves /api/v1/auth/*. Input validation is left to the auth
// service, which owns the rules; the gateway only checks the JSON shape.
type AuthHandler struct {
	client AuthClient
	logger *slog.Logger
}

// NewAuthHandler creates a handler backed by the given auth client.
func NewAuthHandler(client AuthClient, logger *slog.Logger) *AuthHandler {
	return &AuthHandler{client: client, logger: logger}
}

// Register handles POST /api/v1/auth/register.
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decodeJSON(r, &req); err != nil {
		err.write(w)
		return
	}

	resp, err := h.client.Register(r.Context(), &authv1.RegisterRequest{
		Email:    req.Email,
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		h.backendError(w, r, "register", err)
		return
	}

	response.JSON(w, http.StatusCreated, userResponse{
		UserID:   resp.GetUserId(),
		Email:    resp.GetEmail(),
		Username: resp.GetUsername(),
	})
}

// Login handles POST /api/v1/auth/login.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		err.write(w)
		return
	}

	resp, err := h.client.Login(r.Context(), &authv1.LoginRequest{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		h.backendError(w, r, "login", err)
		return
	}

	response.JSON(w, http.StatusOK, newTokenResponse(resp))
}

// Refresh handles POST /api/v1/auth/refresh: it exchanges a refresh token for
// a new token pair. The presented token is rotated out by the auth service.
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshTokenRequest
	if err := decodeJSON(r, &req); err != nil {
		err.write(w)
		return
	}

	resp, err := h.client.RefreshToken(r.Context(), &authv1.RefreshTokenRequest{RefreshToken: req.RefreshToken})
	if err != nil {
		h.backendError(w, r, "refresh_token", err)
		return
	}

	response.JSON(w, http.StatusOK, newTokenResponse(resp))
}

// Logout handles POST /api/v1/auth/logout: it ends the login of the given
// refresh token. It takes no access token, which may already have expired.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	var req refreshTokenRequest
	if err := decodeJSON(r, &req); err != nil {
		err.write(w)
		return
	}

	if _, err := h.client.Logout(r.Context(), &authv1.LogoutRequest{RefreshToken: req.RefreshToken}); err != nil {
		h.backendError(w, r, "logout", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// RevokeAllSessions handles DELETE /api/v1/auth/sessions ("log out
// everywhere"). It must be mounted behind middleware.Authenticate; the auth
// service authorizes the request by the caller's access token, so a client
// can only end its own sessions.
func (h *AuthHandler) RevokeAllSessions(w http.ResponseWriter, r *http.Request) {
	identity, ok := middleware.IdentityFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}

	resp, err := h.client.RevokeAllSessions(r.Context(), &authv1.RevokeAllSessionsRequest{AccessToken: identity.Token})
	if err != nil {
		h.backendError(w, r, "revoke_all_sessions", err)
		return
	}

	response.JSON(w, http.StatusOK, revokeAllSessionsResponse{RevokedCount: resp.GetRevokedCount()})
}

// Me handles GET /api/v1/auth/me and returns the caller's identity. It must
// be mounted behind middleware.Authenticate.
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	identity, ok := middleware.IdentityFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}

	response.JSON(w, http.StatusOK, meResponse{
		UserID:    identity.UserID,
		Email:     identity.Email,
		Username:  identity.Username,
		ExpiresAt: identity.ExpiresAt,
	})
}

// backendError writes the HTTP form of a failed backend call. Server-side
// failures are logged with their cause, which the client never sees.
func (h *AuthHandler) backendError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	statusCode, code, message := response.FromGRPC(err)
	if statusCode >= http.StatusInternalServerError {
		h.logger.ErrorContext(r.Context(), "auth backend call failed", "operation", operation, "error", err)
	}
	response.Error(w, statusCode, code, message)
}
