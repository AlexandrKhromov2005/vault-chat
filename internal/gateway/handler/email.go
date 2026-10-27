package handler

import (
	"net/http"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/response"
)

const emailAccepted = "If the request is eligible, an email will be sent."

type emailPasswordRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type emailRequest struct {
	Email string `json:"email"`
}
type emailTokenRequest struct {
	Token string `json:"token"`
}
type passwordResetRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}
type emailChangeRequest struct {
	Password string `json:"password"`
	NewEmail string `json:"new_email"`
}
type emailMessage struct {
	Message string `json:"message"`
}
type emailCompletion struct {
	Message string `json:"message"`
	UserID  string `json:"user_id"`
}
type accountResponse struct {
	UserID               string     `json:"user_id"`
	Email                string     `json:"email"`
	Username             string     `json:"username"`
	EmailVerified        bool       `json:"email_verified"`
	VerificationRequired bool       `json:"verification_required"`
	VerificationDeadline *time.Time `json:"verification_deadline,omitempty"`
}

// Account returns the caller's private account state; an identity is required.
func (h *AuthHandler) Account(w http.ResponseWriter, r *http.Request) {
	identity, ok := middleware.IdentityFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	account, err := h.client.GetAccount(r.Context(), &authv1.GetAccountRequest{AccessToken: identity.Token})
	if err != nil {
		h.backendError(w, r, "get_account", err)
		return
	}
	if account.GetUserId() == "" || account.GetUserId() != identity.UserID {
		h.backendError(w, r, "get_account", status.Error(codes.Internal, "invalid account response"))
		return
	}
	resp := accountResponse{UserID: account.GetUserId(), Email: account.GetEmail(), Username: account.GetUsername(),
		EmailVerified: account.GetEmailVerified(), VerificationRequired: account.GetVerificationRequired()}
	if deadline := account.GetVerificationDeadline(); deadline != nil {
		if err := deadline.CheckValid(); err != nil {
			h.backendError(w, r, "get_account", status.Error(codes.Internal, "invalid account deadline"))
			return
		}
		value := deadline.AsTime()
		resp.VerificationDeadline = &value
	}
	response.JSON(w, http.StatusOK, resp)
}

// RequestEmailVerification accepts a password-protected resend with a generic response.
func (h *AuthHandler) RequestEmailVerification(w http.ResponseWriter, r *http.Request) {
	var req emailPasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		err.write(w)
		return
	}
	_, err := h.client.RequestEmailVerification(r.Context(), &authv1.RequestEmailVerificationRequest{Email: req.Email, Password: req.Password})
	if err != nil {
		h.backendError(w, r, "request_email_verification", err)
		return
	}
	response.JSON(w, http.StatusAccepted, emailMessage{Message: emailAccepted})
}

// ConfirmEmailVerification requires an explicit POST and returns no login tokens.
func (h *AuthHandler) ConfirmEmailVerification(w http.ResponseWriter, r *http.Request) {
	var req emailTokenRequest
	if err := decodeJSON(r, &req); err != nil {
		err.write(w)
		return
	}
	resp, err := h.client.ConfirmEmailVerification(r.Context(), &authv1.ConfirmEmailVerificationRequest{Token: req.Token})
	if err != nil {
		h.backendError(w, r, "confirm_email_verification", err)
		return
	}
	h.completeEmail(w, r, resp.GetUserId(), "Email verified.")
}

// RequestPasswordReset acknowledges eligible and unknown accounts identically.
func (h *AuthHandler) RequestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var req emailRequest
	if err := decodeJSON(r, &req); err != nil {
		err.write(w)
		return
	}
	_, err := h.client.RequestPasswordReset(r.Context(), &authv1.RequestPasswordResetRequest{Email: req.Email})
	if err != nil {
		h.backendError(w, r, "request_password_reset", err)
		return
	}
	response.JSON(w, http.StatusAccepted, emailMessage{Message: emailAccepted})
}

// ConfirmPasswordReset sets a new password and identifies only the affected owner.
func (h *AuthHandler) ConfirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	var req passwordResetRequest
	if err := decodeJSON(r, &req); err != nil {
		err.write(w)
		return
	}
	resp, err := h.client.ConfirmPasswordReset(r.Context(), &authv1.ConfirmPasswordResetRequest{Token: req.Token, Password: req.Password})
	if err != nil {
		h.backendError(w, r, "confirm_password_reset", err)
		return
	}
	h.completeEmail(w, r, resp.GetUserId(), "Password reset.")
}

// RequestEmailChange derives identity only from authenticated middleware state.
func (h *AuthHandler) RequestEmailChange(w http.ResponseWriter, r *http.Request) {
	identity, ok := middleware.IdentityFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	var req emailChangeRequest
	if err := decodeJSON(r, &req); err != nil {
		err.write(w)
		return
	}
	_, err := h.client.RequestEmailChange(r.Context(), &authv1.RequestEmailChangeRequest{AccessToken: identity.Token, Password: req.Password, NewEmail: req.NewEmail})
	if err != nil {
		h.backendError(w, r, "request_email_change", err)
		return
	}
	response.JSON(w, http.StatusAccepted, emailMessage{Message: emailAccepted})
}

// ConfirmEmailChange applies a new-address proof without authenticating the browser.
func (h *AuthHandler) ConfirmEmailChange(w http.ResponseWriter, r *http.Request) {
	var req emailTokenRequest
	if err := decodeJSON(r, &req); err != nil {
		err.write(w)
		return
	}
	resp, err := h.client.ConfirmEmailChange(r.Context(), &authv1.ConfirmEmailChangeRequest{Token: req.Token})
	if err != nil {
		h.backendError(w, r, "confirm_email_change", err)
		return
	}
	h.completeEmail(w, r, resp.GetUserId(), "Email changed.")
}

func (h *AuthHandler) completeEmail(w http.ResponseWriter, r *http.Request, owner, message string) {
	if owner == "" {
		h.backendError(w, r, "complete_email_action", status.Error(codes.Internal, "missing action owner"))
		return
	}
	response.JSON(w, http.StatusOK, emailCompletion{Message: message, UserID: owner})
}

func emailHTTPError(err error) (int, string, string, bool) {
	s := status.Convert(err)
	for _, detail := range s.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if !ok || info.GetDomain() != "auth.v1" {
			continue
		}
		if s.Code() == codes.FailedPrecondition && info.GetReason() == "EMAIL_VERIFICATION_REQUIRED" {
			return http.StatusForbidden, "email_verification_required", "email verification required", true
		}
		if s.Code() == codes.InvalidArgument && info.GetReason() == "INVALID_EMAIL_ACTION" {
			return http.StatusBadRequest, "invalid_email_action", "invalid email action", true
		}
	}
	return 0, "", "", false
}
