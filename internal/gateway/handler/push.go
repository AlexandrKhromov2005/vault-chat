package handler

import (
	"net/http"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/response"
)

type pushTokenRequest struct {
	Provider string `json:"provider"`
	Token    string `json:"token"`
}

// RegisterPushToken handles PUT /api/v1/push/token behind
// middleware.Authenticate. Auth binds the token to the caller's login and
// validates it (docs/push.md).
func (h *AuthHandler) RegisterPushToken(w http.ResponseWriter, r *http.Request) {
	identity, ok := middleware.IdentityFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	var req pushTokenRequest
	if err := decodeJSON(r, &req); err != nil {
		err.write(w)
		return
	}
	_, err := h.client.RegisterPushToken(r.Context(), &authv1.RegisterPushTokenRequest{
		AccessToken: identity.Token, Provider: req.Provider, Token: req.Token,
	})
	if err != nil {
		h.backendError(w, r, "register_push_token", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UnregisterPushToken handles DELETE /api/v1/push/token behind
// middleware.Authenticate; it succeeds when the login has no token.
func (h *AuthHandler) UnregisterPushToken(w http.ResponseWriter, r *http.Request) {
	identity, ok := middleware.IdentityFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	_, err := h.client.UnregisterPushToken(r.Context(), &authv1.UnregisterPushTokenRequest{AccessToken: identity.Token})
	if err != nil {
		h.backendError(w, r, "unregister_push_token", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
