package response_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/response"
)

func TestJSON(t *testing.T) {
	rec := httptest.NewRecorder()

	response.JSON(rec, http.StatusCreated, map[string]string{"user_id": "u-1"})

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{"user_id":"u-1"}`, rec.Body.String())
}

func TestError(t *testing.T) {
	rec := httptest.NewRecorder()

	response.Error(rec, http.StatusBadRequest, "invalid_json", "malformed body")

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{"error":{"code":"invalid_json","message":"malformed body"}}`, rec.Body.String())
}

func TestFromGRPC(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{"invalid argument", status.Error(codes.InvalidArgument, "invalid email address"),
			http.StatusBadRequest, "invalid_argument", "invalid email address"},
		{"unauthenticated", status.Error(codes.Unauthenticated, "invalid credentials"),
			http.StatusUnauthorized, "unauthenticated", "invalid credentials"},
		{"permission denied", status.Error(codes.PermissionDenied, "not a member"),
			http.StatusForbidden, "permission_denied", "not a member"},
		{"not found", status.Error(codes.NotFound, "no such user"),
			http.StatusNotFound, "not_found", "no such user"},
		{"already exists", status.Error(codes.AlreadyExists, "email already registered"),
			http.StatusConflict, "already_exists", "email already registered"},
		{"resource exhausted", status.Error(codes.ResourceExhausted, "quota exceeded"),
			http.StatusTooManyRequests, "rate_limited", "quota exceeded"},
		// Server-side failures never leak upstream messages to clients.
		{"deadline exceeded", status.Error(codes.DeadlineExceeded, "context deadline exceeded on auth:50051"),
			http.StatusGatewayTimeout, "timeout", "upstream service timed out"},
		{"unavailable", status.Error(codes.Unavailable, "connection refused 10.0.0.5:50051"),
			http.StatusServiceUnavailable, "unavailable", "upstream service unavailable"},
		{"canceled", status.Error(codes.Canceled, "context canceled"),
			http.StatusServiceUnavailable, "unavailable", "upstream service unavailable"},
		{"internal", status.Error(codes.Internal, "pq: relation users does not exist"),
			http.StatusInternalServerError, "internal", "internal error"},
		{"unknown code", status.Error(codes.DataLoss, "disk on fire"),
			http.StatusInternalServerError, "internal", "internal error"},
		{"plain error", errors.New("boom"),
			http.StatusInternalServerError, "internal", "internal error"},
		{"bare context deadline", context.DeadlineExceeded,
			http.StatusGatewayTimeout, "timeout", "upstream service timed out"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStatus, gotCode, gotMessage := response.FromGRPC(tt.err)
			require.Equal(t, tt.wantStatus, gotStatus)
			require.Equal(t, tt.wantCode, gotCode)
			require.Equal(t, tt.wantMessage, gotMessage)
		})
	}
}

func TestGRPCError(t *testing.T) {
	rec := httptest.NewRecorder()

	response.GRPCError(rec, status.Error(codes.AlreadyExists, "username already taken"))

	require.Equal(t, http.StatusConflict, rec.Code)
	var body response.ErrorBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "already_exists", body.Error.Code)
	require.Equal(t, "username already taken", body.Error.Message)
}
