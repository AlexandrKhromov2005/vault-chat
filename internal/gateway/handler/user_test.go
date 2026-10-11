package handler_test

import (
	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUserREST_RequiresIdentityAndUsesPublicProfile(t *testing.T) {
	client := mocks.NewMockAuthClient(t)
	h := handler.NewAuthHandler(client, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	rec := httptest.NewRecorder()
	h.User(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, 401, rec.Code)
	req := chatRequest(http.MethodGet, "")
	req.SetPathValue("user_id", "target")
	client.EXPECT().GetUser(mock.Anything, &authv1.GetUserRequest{AccessToken: "access", UserId: "target"}).Return(&authv1.GetUserResponse{UserId: "target", Username: "bob"}, nil).Once()
	rec = httptest.NewRecorder()
	h.User(rec, req)
	require.Equal(t, 200, rec.Code)
	require.JSONEq(t, `{"user_id":"target","username":"bob"}`, rec.Body.String())
}
