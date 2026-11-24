package router_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	callsv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/calls/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	handlermocks "github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler/mocks"
	middlewaremocks "github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware/mocks"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/ratelimit"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/router"
)

type activeCallsClient struct{ callsv1.CallsServiceClient }

func (activeCallsClient) ListActive(context.Context, *callsv1.ListActiveRequest, ...grpc.CallOption) (*callsv1.ListActiveResponse, error) {
	return &callsv1.ListActiveResponse{Calls: []*callsv1.Call{}}, nil
}

type recordingCallsLimiter struct {
	mu   sync.Mutex
	keys []string
}

func (l *recordingCallsLimiter) Allow(_ context.Context, key string) (ratelimit.Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.keys = append(l.keys, key)
	return ratelimit.Result{Allowed: true, Limit: 1200, Remaining: 1199}, nil
}

func (l *recordingCallsLimiter) recorded() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.keys...)
}

func TestRouter_CallsUseDedicatedQuota(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	validator := middlewaremocks.NewMockTokenValidator(t)
	validator.EXPECT().ValidateToken(mock.Anything, mock.Anything).Return(&authv1.ValidateTokenResponse{
		Valid: true, UserId: "user-1", SessionId: "session-1",
	}, nil).Twice()
	general := middlewaremocks.NewMockLimiter(t)
	calls := &recordingCallsLimiter{}
	h := router.New(router.Deps{
		Auth:           handler.NewAuthHandler(handlermocks.NewMockAuthClient(t), logger),
		Health:         handler.NewHealth(nil, logger),
		Calls:          handler.NewCallsHandler(activeCallsClient{}, logger),
		CallsLimiter:   calls,
		TokenValidator: validator,
		Limiter:        general,
		Logger:         logger,
		MaxBodyBytes:   1 << 20,
		RequestTimeout: time.Second,
	})

	for range 2 {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/calls?active=true", nil)
		req.Header.Set("Authorization", "Bearer access-1")
		req.RemoteAddr = "203.0.113.7:5555"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{"calls":[]}`, rec.Body.String())
	}
	require.Equal(t, []string{"ip:203.0.113.7", "user:user-1", "ip:203.0.113.7", "user:user-1"}, calls.recorded())
}
