package handler_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler/mocks"
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

func TestUsersREST_ForwardsQueryAndReturnsPublicProfiles(t *testing.T) {
	client := mocks.NewMockAuthClient(t)
	h := handler.NewAuthHandler(client, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	rec := httptest.NewRecorder()
	h.Users(rec, httptest.NewRequest(http.MethodGet, "/?query=bo", nil))
	require.Equal(t, 401, rec.Code)

	client.EXPECT().SearchUsers(mock.Anything, &authv1.SearchUsersRequest{AccessToken: "access", Query: "Bo", Limit: 5}).
		Return(&authv1.SearchUsersResponse{Users: []*authv1.UserProfile{{UserId: "id-1", Username: "bob"}}}, nil).Once()
	req := chatRequest(http.MethodGet, "")
	req.URL.RawQuery = "query=Bo&limit=5"
	rec = httptest.NewRecorder()
	h.Users(rec, req)
	require.Equal(t, 200, rec.Code)
	require.JSONEq(t, `{"users":[{"user_id":"id-1","username":"bob"}]}`, rec.Body.String())

	client.EXPECT().SearchUsers(mock.Anything, &authv1.SearchUsersRequest{AccessToken: "access", Query: "zz"}).
		Return(&authv1.SearchUsersResponse{}, nil).Once()
	req.URL.RawQuery = "query=zz"
	rec = httptest.NewRecorder()
	h.Users(rec, req)
	require.Equal(t, 200, rec.Code)
	require.JSONEq(t, `{"users":[]}`, rec.Body.String())
}

func TestUsersREST_RejectsAmbiguousQueries(t *testing.T) {
	client := mocks.NewMockAuthClient(t)
	h := handler.NewAuthHandler(client, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	for _, query := range []string{"", "limit=5", "query=a&query=b", "query=a&user_id=x", "query=%zz", "query=a&limit=0", "query=a&limit=51", "query=a&limit=01", "query=a&limit=", "query=a&limit=1&limit=2", "query=a&limit=x"} {
		req := chatRequest(http.MethodGet, "")
		req.URL.RawQuery = query
		rec := httptest.NewRecorder()
		h.Users(rec, req)
		require.Equal(t, 400, rec.Code, query)
	}
	require.Empty(t, client.Calls)
}
