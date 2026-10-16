package handler

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/response"
)

type publicProfile struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
}

// Users searches the user directory by username prefix for an authenticated
// caller. Query rules are owned by Auth; the gateway only checks the shape.
func (h *AuthHandler) Users(w http.ResponseWriter, r *http.Request) {
	identity, ok := middleware.IdentityFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	query, limit, err := userSearchQuery(r.URL.RawQuery)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "invalid_argument", "invalid user search query")
		return
	}
	resp, err := h.client.SearchUsers(r.Context(), &authv1.SearchUsersRequest{AccessToken: identity.Token, Query: query, Limit: limit})
	if err != nil {
		h.backendError(w, r, "search_users", err)
		return
	}
	page := struct {
		Users []publicProfile `json:"users"`
	}{Users: make([]publicProfile, 0, len(resp.GetUsers()))}
	for _, user := range resp.GetUsers() {
		page.Users = append(page.Users, publicProfile{user.GetUserId(), user.GetUsername()})
	}
	response.JSON(w, http.StatusOK, page)
}

// userSearchQuery accepts exactly one query parameter and at most one
// canonical decimal limit from 1 to 50; a missing limit is returned as 0.
func userSearchQuery(raw string) (string, int32, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "", 0, err
	}
	for key, value := range values {
		if (key != "query" && key != "limit") || len(value) != 1 {
			return "", 0, errors.New("unexpected user search parameter")
		}
	}
	query, ok := values["query"]
	if !ok {
		return "", 0, errors.New("missing user search query")
	}
	var limit int64
	if value, ok := values["limit"]; ok {
		limit, err = strconv.ParseInt(value[0], 10, 32)
		if err != nil || limit < 1 || limit > 50 || strconv.FormatInt(limit, 10) != value[0] {
			return "", 0, errors.New("invalid user search limit")
		}
	}
	return query[0], int32(limit), nil
}
