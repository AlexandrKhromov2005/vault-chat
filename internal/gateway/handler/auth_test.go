package handler_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler/mocks"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
)

func newJSONRequest(target, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// failingReader fails every read with err.
type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func newAuthHandler(t *testing.T) (*handler.AuthHandler, *mocks.MockAuthClient, *bytes.Buffer) {
	t.Helper()
	client := mocks.NewMockAuthClient(t)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	return handler.NewAuthHandler(client, logger), client, &logs
}

func TestAuthHandler_Register(t *testing.T) {
	t.Run("created", func(t *testing.T) {
		h, client, _ := newAuthHandler(t)
		client.EXPECT().
			Register(mock.Anything, &authv1.RegisterRequest{
				Email: "user@example.com", Username: "username1", Password: "password1",
			}).
			Return(&authv1.RegisterResponse{UserId: "u-1", Email: "user@example.com", Username: "username1"}, nil)

		rec := httptest.NewRecorder()
		h.Register(rec, newJSONRequest("/api/v1/auth/register",
			`{"email":"user@example.com","username":"username1","password":"password1"}`))

		require.Equal(t, http.StatusCreated, rec.Code)
		require.JSONEq(t, `{"user_id":"u-1","email":"user@example.com","username":"username1"}`, rec.Body.String())
	})

	t.Run("backend client errors are mapped", func(t *testing.T) {
		tests := map[codes.Code]int{
			codes.InvalidArgument: http.StatusBadRequest,
			codes.AlreadyExists:   http.StatusConflict,
		}
		for code, want := range tests {
			h, client, _ := newAuthHandler(t)
			client.EXPECT().Register(mock.Anything, mock.Anything).Return(nil, status.Error(code, "rejected"))

			rec := httptest.NewRecorder()
			h.Register(rec, newJSONRequest("/", `{"email":"x"}`))
			require.Equal(t, want, rec.Code, code.String())
		}
	})

	t.Run("backend failures are logged with the cause", func(t *testing.T) {
		h, client, logs := newAuthHandler(t)
		client.EXPECT().Register(mock.Anything, mock.Anything).
			Return(nil, status.Error(codes.Unavailable, "dial tcp 10.0.0.5:50051: connection refused"))

		rec := httptest.NewRecorder()
		h.Register(rec, newJSONRequest("/", `{}`))

		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.NotContains(t, rec.Body.String(), "10.0.0.5")
		require.Contains(t, logs.String(), "connection refused")
	})
}

func TestAuthHandler_RejectsBadBodies(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
		wantCode    string
	}{
		{"wrong content type", "text/plain", `{}`, http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"missing content type", "", `{}`, http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"empty body", "application/json", ``, http.StatusBadRequest, "invalid_json"},
		{"malformed", "application/json", `{"password":`, http.StatusBadRequest, "invalid_json"},
		{"unknown field", "application/json", `{"emial":"x"}`, http.StatusBadRequest, "invalid_json"},
		{"trailing data", "application/json", `{} {}`, http.StatusBadRequest, "invalid_json"},
		{"array", "application/json", `[]`, http.StatusBadRequest, "invalid_json"},
	}
	endpoints := map[string]func(*handler.AuthHandler) http.HandlerFunc{
		"register": func(h *handler.AuthHandler) http.HandlerFunc { return h.Register },
		"login":    func(h *handler.AuthHandler) http.HandlerFunc { return h.Login },
		"refresh":  func(h *handler.AuthHandler) http.HandlerFunc { return h.Refresh },
		"logout":   func(h *handler.AuthHandler) http.HandlerFunc { return h.Logout },
	}
	for endpoint, handlerFunc := range endpoints {
		for _, tt := range tests {
			t.Run(endpoint+"/"+tt.name, func(t *testing.T) {
				h, _, _ := newAuthHandler(t) // the mock fails the test if auth is called

				req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
				if tt.contentType != "" {
					req.Header.Set("Content-Type", tt.contentType)
				}
				rec := httptest.NewRecorder()
				handlerFunc(h)(rec, req)

				require.Equal(t, tt.wantStatus, rec.Code)
				require.Contains(t, rec.Body.String(), `"code":"`+tt.wantCode+`"`)
			})
		}
	}

	t.Run("content type with charset is accepted", func(t *testing.T) {
		h, client, _ := newAuthHandler(t)
		client.EXPECT().Login(mock.Anything, mock.Anything).Return(&authv1.LoginResponse{}, nil)

		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		rec := httptest.NewRecorder()
		h.Login(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("body not received before the deadline", func(t *testing.T) {
		h, _, _ := newAuthHandler(t) // the mock fails the test if auth is called

		req := newJSONRequest("/", "")
		// What a connection read deadline produces mid-body.
		req.Body = io.NopCloser(failingReader{&net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}})
		rec := httptest.NewRecorder()
		h.Login(rec, req)

		require.Equal(t, http.StatusRequestTimeout, rec.Code)
		require.JSONEq(t, `{"error":{"code":"request_timeout","message":"request body was not received in time"}}`,
			rec.Body.String())
	})

	t.Run("oversized body", func(t *testing.T) {
		h, _, _ := newAuthHandler(t)

		rec := httptest.NewRecorder()
		req := newJSONRequest("/", `{"email":"`+strings.Repeat("a", 64)+`"}`)
		req.Body = http.MaxBytesReader(rec, req.Body, 16)
		h.Register(rec, req)

		require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	})
}

func TestAuthHandler_Login(t *testing.T) {
	accessExpiresAt := time.Date(2026, 10, 7, 12, 15, 0, 0, time.UTC)
	refreshExpiresAt := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	t.Run("issues tokens", func(t *testing.T) {
		h, client, _ := newAuthHandler(t)
		client.EXPECT().
			Login(mock.Anything, &authv1.LoginRequest{Email: "user@example.com", Password: "password1"}).
			Return(&authv1.LoginResponse{
				UserId:           "u-1",
				AccessToken:      "access",
				RefreshToken:     "refresh",
				AccessExpiresAt:  timestamppb.New(accessExpiresAt),
				RefreshExpiresAt: timestamppb.New(refreshExpiresAt),
			}, nil)

		rec := httptest.NewRecorder()
		h.Login(rec, newJSONRequest("/api/v1/auth/login",
			`{"email":"user@example.com","password":"password1"}`))

		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{
			"user_id": "u-1",
			"token_type": "Bearer",
			"access_token": "access",
			"refresh_token": "refresh",
			"access_expires_at": "2026-10-07T12:15:00Z",
			"refresh_expires_at": "2026-11-06T12:00:00Z"
		}`, rec.Body.String())
	})

	t.Run("wrong credentials", func(t *testing.T) {
		h, client, _ := newAuthHandler(t)
		client.EXPECT().Login(mock.Anything, mock.Anything).
			Return(nil, status.Error(codes.Unauthenticated, "invalid credentials"))

		rec := httptest.NewRecorder()
		h.Login(rec, newJSONRequest("/", `{"email":"user@example.com","password":"nope"}`))

		require.Equal(t, http.StatusUnauthorized, rec.Code)
		require.JSONEq(t, `{"error":{"code":"unauthenticated","message":"invalid credentials"}}`, rec.Body.String())
	})
}

func TestAuthHandler_Me(t *testing.T) {
	t.Run("returns the authenticated identity", func(t *testing.T) {
		h, _, _ := newAuthHandler(t)
		ctx := middleware.WithIdentity(context.Background(), middleware.Identity{
			UserID:    "u-1",
			Email:     "user@example.com",
			Username:  "username1",
			ExpiresAt: time.Date(2026, 10, 7, 12, 15, 0, 0, time.UTC),
		})

		rec := httptest.NewRecorder()
		h.Me(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil).WithContext(ctx))

		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{
			"user_id": "u-1",
			"email": "user@example.com",
			"username": "username1",
			"expires_at": "2026-10-07T12:15:00Z"
		}`, rec.Body.String())
	})

	t.Run("unauthenticated request is rejected", func(t *testing.T) {
		h, _, _ := newAuthHandler(t)

		rec := httptest.NewRecorder()
		h.Me(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil))

		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

func TestAuthHandler_Refresh(t *testing.T) {
	t.Run("rotates the token pair", func(t *testing.T) {
		h, client, _ := newAuthHandler(t)
		client.EXPECT().
			RefreshToken(mock.Anything, &authv1.RefreshTokenRequest{RefreshToken: "refresh-1"}).
			Return(&authv1.RefreshTokenResponse{
				UserId:           "u-1",
				AccessToken:      "access-2",
				RefreshToken:     "refresh-2",
				AccessExpiresAt:  timestamppb.New(time.Date(2026, 10, 7, 12, 15, 0, 0, time.UTC)),
				RefreshExpiresAt: timestamppb.New(time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)),
			}, nil)

		rec := httptest.NewRecorder()
		h.Refresh(rec, newJSONRequest("/api/v1/auth/refresh", `{"refresh_token":"refresh-1"}`))

		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{
			"user_id": "u-1",
			"token_type": "Bearer",
			"access_token": "access-2",
			"refresh_token": "refresh-2",
			"access_expires_at": "2026-10-07T12:15:00Z",
			"refresh_expires_at": "2026-11-06T12:00:00Z"
		}`, rec.Body.String())
	})

	t.Run("rejected refresh token", func(t *testing.T) {
		h, client, _ := newAuthHandler(t)
		client.EXPECT().RefreshToken(mock.Anything, mock.Anything).
			Return(nil, status.Error(codes.Unauthenticated, "invalid token"))

		rec := httptest.NewRecorder()
		h.Refresh(rec, newJSONRequest("/", `{"refresh_token":"reused"}`))

		require.Equal(t, http.StatusUnauthorized, rec.Code)
		require.JSONEq(t, `{"error":{"code":"unauthenticated","message":"invalid token"}}`, rec.Body.String())
	})
}

func TestAuthHandler_Logout(t *testing.T) {
	t.Run("ends the session", func(t *testing.T) {
		h, client, _ := newAuthHandler(t)
		client.EXPECT().
			Logout(mock.Anything, &authv1.LogoutRequest{RefreshToken: "refresh-1"}).
			Return(&authv1.LogoutResponse{}, nil)

		rec := httptest.NewRecorder()
		h.Logout(rec, newJSONRequest("/api/v1/auth/logout", `{"refresh_token":"refresh-1"}`))

		require.Equal(t, http.StatusNoContent, rec.Code)
		require.Empty(t, rec.Body.String())
	})

	t.Run("rejected refresh token", func(t *testing.T) {
		h, client, _ := newAuthHandler(t)
		client.EXPECT().Logout(mock.Anything, mock.Anything).
			Return(nil, status.Error(codes.Unauthenticated, "invalid token"))

		rec := httptest.NewRecorder()
		h.Logout(rec, newJSONRequest("/", `{"refresh_token":"garbage"}`))

		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

func TestAuthHandler_RevokeAllSessions(t *testing.T) {
	t.Run("forwards the caller's access token", func(t *testing.T) {
		h, client, _ := newAuthHandler(t)
		client.EXPECT().
			RevokeAllSessions(mock.Anything, &authv1.RevokeAllSessionsRequest{AccessToken: "access-1"}).
			Return(&authv1.RevokeAllSessionsResponse{RevokedCount: 3}, nil)
		ctx := middleware.WithIdentity(context.Background(), middleware.Identity{UserID: "u-1", Token: "access-1"})

		rec := httptest.NewRecorder()
		h.RevokeAllSessions(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/auth/sessions", nil).WithContext(ctx))

		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{"revoked_count":3}`, rec.Body.String())
	})

	t.Run("unauthenticated request is rejected without calling auth", func(t *testing.T) {
		h, _, _ := newAuthHandler(t)

		rec := httptest.NewRecorder()
		h.RevokeAllSessions(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/auth/sessions", nil))

		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("backend failure", func(t *testing.T) {
		h, client, logs := newAuthHandler(t)
		client.EXPECT().RevokeAllSessions(mock.Anything, mock.Anything).
			Return(nil, status.Error(codes.Internal, "internal error"))
		ctx := middleware.WithIdentity(context.Background(), middleware.Identity{UserID: "u-1", Token: "access-1"})

		rec := httptest.NewRecorder()
		h.RevokeAllSessions(rec, httptest.NewRequest(http.MethodDelete, "/", nil).WithContext(ctx))

		require.Equal(t, http.StatusInternalServerError, rec.Code)
		require.Contains(t, logs.String(), "revoke_all_sessions")
	})
}
