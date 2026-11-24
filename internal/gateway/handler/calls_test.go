package handler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	callsv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/calls/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
)

type callsClientStub struct {
	last     any
	response any
	err      error
}

func (c *callsClientStub) result(_ context.Context, req any, _ ...grpc.CallOption) (any, error) {
	c.last = req
	return c.response, c.err
}
func callResult[T any](response any, err error) (*T, error) {
	if err != nil {
		return nil, err
	}
	result, _ := response.(*T)
	return result, nil
}

func (c *callsClientStub) Create(ctx context.Context, req *callsv1.CreateRequest, opts ...grpc.CallOption) (*callsv1.CreateResponse, error) {
	r, err := c.result(ctx, req, opts...)
	return callResult[callsv1.CreateResponse](r, err)
}
func (c *callsClientStub) Accept(ctx context.Context, req *callsv1.AcceptRequest, opts ...grpc.CallOption) (*callsv1.AcceptResponse, error) {
	r, err := c.result(ctx, req, opts...)
	return callResult[callsv1.AcceptResponse](r, err)
}
func (c *callsClientStub) Reject(ctx context.Context, req *callsv1.RejectRequest, opts ...grpc.CallOption) (*callsv1.RejectResponse, error) {
	r, err := c.result(ctx, req, opts...)
	return callResult[callsv1.RejectResponse](r, err)
}
func (c *callsClientStub) Cancel(ctx context.Context, req *callsv1.CancelRequest, opts ...grpc.CallOption) (*callsv1.CancelResponse, error) {
	r, err := c.result(ctx, req, opts...)
	return callResult[callsv1.CancelResponse](r, err)
}
func (c *callsClientStub) Connected(ctx context.Context, req *callsv1.ConnectedRequest, opts ...grpc.CallOption) (*callsv1.ConnectedResponse, error) {
	r, err := c.result(ctx, req, opts...)
	return callResult[callsv1.ConnectedResponse](r, err)
}
func (c *callsClientStub) Hangup(ctx context.Context, req *callsv1.HangupRequest, opts ...grpc.CallOption) (*callsv1.HangupResponse, error) {
	r, err := c.result(ctx, req, opts...)
	return callResult[callsv1.HangupResponse](r, err)
}
func (c *callsClientStub) Heartbeat(ctx context.Context, req *callsv1.HeartbeatRequest, opts ...grpc.CallOption) (*callsv1.HeartbeatResponse, error) {
	r, err := c.result(ctx, req, opts...)
	return callResult[callsv1.HeartbeatResponse](r, err)
}
func (c *callsClientStub) Signal(ctx context.Context, req *callsv1.SignalRequest, opts ...grpc.CallOption) (*callsv1.SignalResponse, error) {
	r, err := c.result(ctx, req, opts...)
	return callResult[callsv1.SignalResponse](r, err)
}
func (c *callsClientStub) Get(ctx context.Context, req *callsv1.GetRequest, opts ...grpc.CallOption) (*callsv1.GetResponse, error) {
	r, err := c.result(ctx, req, opts...)
	return callResult[callsv1.GetResponse](r, err)
}
func (c *callsClientStub) ListActive(ctx context.Context, req *callsv1.ListActiveRequest, opts ...grpc.CallOption) (*callsv1.ListActiveResponse, error) {
	r, err := c.result(ctx, req, opts...)
	return callResult[callsv1.ListActiveResponse](r, err)
}
func (c *callsClientStub) Signals(ctx context.Context, req *callsv1.SignalsRequest, opts ...grpc.CallOption) (*callsv1.SignalsResponse, error) {
	r, err := c.result(ctx, req, opts...)
	return callResult[callsv1.SignalsResponse](r, err)
}
func (c *callsClientStub) Turn(ctx context.Context, req *callsv1.TurnRequest, opts ...grpc.CallOption) (*callsv1.TurnResponse, error) {
	r, err := c.result(ctx, req, opts...)
	return callResult[callsv1.TurnResponse](r, err)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func callRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("call_id", "call-1")
	return r.WithContext(middleware.WithIdentity(r.Context(), middleware.Identity{UserID: "user-1", SessionID: "session-1", Token: "access-1"}))
}

func TestCallsCreateUsesValidatedSession(t *testing.T) {
	client := &callsClientStub{response: &callsv1.CreateResponse{Call: &callsv1.Call{CallId: "call-1", RoomId: "room-1", State: "ringing"}}}
	recorder := httptest.NewRecorder()
	NewCallsHandler(client, discardLogger()).Create(recorder, callRequest(http.MethodPost, "/api/v1/calls", `{"room_id":"room-1","command_id":"command-1"}`))
	require.Equal(t, http.StatusCreated, recorder.Code)
	request := client.last.(*callsv1.CreateRequest)
	require.Equal(t, "session-1", request.GetIdentity().GetSessionId())
	require.Equal(t, "user-1", request.GetIdentity().GetUserId())
	require.Equal(t, "access-1", request.GetIdentity().GetAccessToken())
	require.JSONEq(t, `{"call_id":"call-1","room_id":"room-1","state":"ringing","state_version":0,"caller_id":"","callee_id":""}`, recorder.Body.String())
}

func TestCallsRejectsClientSuppliedIdentity(t *testing.T) {
	client := &callsClientStub{}
	recorder := httptest.NewRecorder()
	NewCallsHandler(client, discardLogger()).Create(recorder, callRequest(http.MethodPost, "/api/v1/calls", `{"room_id":"room-1","command_id":"command-1","session_id":"forged"}`))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Nil(t, client.last)
}

func TestCallsSignalAndTurn(t *testing.T) {
	logger := discardLogger()
	t.Run("signal", func(t *testing.T) {
		client := &callsClientStub{response: &callsv1.SignalResponse{State: "connecting", SignalSeq: 7}}
		recorder := httptest.NewRecorder()
		NewCallsHandler(client, logger).Signal(recorder, callRequest(http.MethodPost, "/api/v1/calls/call-1/signal", `{"kind":"offer","payload":"sdp","sender_seq":3}`))
		require.Equal(t, http.StatusOK, recorder.Code)
		request := client.last.(*callsv1.SignalRequest)
		require.Equal(t, "call-1", request.GetCallId())
		require.Equal(t, int64(3), request.GetSenderSeq())
		require.JSONEq(t, `{"state":"connecting","signal_seq":7}`, recorder.Body.String())
	})
	t.Run("turn", func(t *testing.T) {
		client := &callsClientStub{response: &callsv1.TurnResponse{Ttl: 600, IceServers: []*callsv1.IceServer{{Urls: []string{"turn:turn.internal:3478"}, Username: "u", Credential: "p"}}}}
		recorder := httptest.NewRecorder()
		NewCallsHandler(client, logger).Turn(recorder, callRequest(http.MethodPost, "/api/v1/calls/call-1/turn", `{}`))
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, "call-1", client.last.(*callsv1.TurnRequest).GetCallId())
		require.JSONEq(t, `{"ttl":600,"ice_servers":[{"urls":["turn:turn.internal:3478"],"username":"u","credential":"p"}]}`, recorder.Body.String())
	})
}

func TestCallsActiveAndSignals(t *testing.T) {
	logger := discardLogger()
	t.Run("active", func(t *testing.T) {
		client := &callsClientStub{response: &callsv1.ListActiveResponse{Calls: []*callsv1.Call{{CallId: "call-1", State: "active"}}}}
		recorder := httptest.NewRecorder()
		NewCallsHandler(client, logger).Active(recorder, callRequest(http.MethodGet, "/api/v1/calls?active=true", ""))
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Contains(t, recorder.Body.String(), `"calls":[`)
	})
	t.Run("signals", func(t *testing.T) {
		client := &callsClientStub{response: &callsv1.SignalsResponse{Signals: []*callsv1.Signal{{SignalSeq: 2, SenderSeq: 1, Kind: "answer", Payload: "sdp"}}}}
		recorder := httptest.NewRecorder()
		NewCallsHandler(client, logger).Signals(recorder, callRequest(http.MethodGet, "/api/v1/calls/call-1/signals?after=1", ""))
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, int64(1), client.last.(*callsv1.SignalsRequest).GetAfter())
		require.Contains(t, recorder.Body.String(), `"signal_seq":2`)
	})
	t.Run("invalid cursor", func(t *testing.T) {
		client := &callsClientStub{}
		recorder := httptest.NewRecorder()
		NewCallsHandler(client, logger).Signals(recorder, callRequest(http.MethodGet, "/api/v1/calls/call-1/signals?after=-1", ""))
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Nil(t, client.last)
	})
}

func TestCallsCapacityHasRetryAfter(t *testing.T) {
	client := &callsClientStub{err: status.Error(codes.Unavailable, "capacity")}
	recorder := httptest.NewRecorder()
	NewCallsHandler(client, discardLogger()).Create(recorder, callRequest(http.MethodPost, "/api/v1/calls", `{"room_id":"room-1","command_id":"command-1"}`))
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Equal(t, "5", recorder.Header().Get("Retry-After"))
}
