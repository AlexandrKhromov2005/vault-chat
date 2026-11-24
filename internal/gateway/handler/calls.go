package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	callsv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/calls/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/response"
)

// CallsClient is the internal Calls RPC API consumed by Gateway.
type CallsClient interface {
	Create(context.Context, *callsv1.CreateRequest, ...grpc.CallOption) (*callsv1.CreateResponse, error)
	Accept(context.Context, *callsv1.AcceptRequest, ...grpc.CallOption) (*callsv1.AcceptResponse, error)
	Reject(context.Context, *callsv1.RejectRequest, ...grpc.CallOption) (*callsv1.RejectResponse, error)
	Cancel(context.Context, *callsv1.CancelRequest, ...grpc.CallOption) (*callsv1.CancelResponse, error)
	Connected(context.Context, *callsv1.ConnectedRequest, ...grpc.CallOption) (*callsv1.ConnectedResponse, error)
	Hangup(context.Context, *callsv1.HangupRequest, ...grpc.CallOption) (*callsv1.HangupResponse, error)
	Heartbeat(context.Context, *callsv1.HeartbeatRequest, ...grpc.CallOption) (*callsv1.HeartbeatResponse, error)
	Signal(context.Context, *callsv1.SignalRequest, ...grpc.CallOption) (*callsv1.SignalResponse, error)
	Get(context.Context, *callsv1.GetRequest, ...grpc.CallOption) (*callsv1.GetResponse, error)
	ListActive(context.Context, *callsv1.ListActiveRequest, ...grpc.CallOption) (*callsv1.ListActiveResponse, error)
	Signals(context.Context, *callsv1.SignalsRequest, ...grpc.CallOption) (*callsv1.SignalsResponse, error)
	Turn(context.Context, *callsv1.TurnRequest, ...grpc.CallOption) (*callsv1.TurnResponse, error)
}

// CallsHandler translates authenticated REST requests into Calls RPC calls.
type CallsHandler struct {
	client CallsClient
	logger *slog.Logger
}

func NewCallsHandler(client CallsClient, logger *slog.Logger) *CallsHandler {
	return &CallsHandler{client: client, logger: logger}
}

type callResponse struct {
	CallID       string `json:"call_id"`
	RoomID       string `json:"room_id"`
	State        string `json:"state"`
	StateVersion int64  `json:"state_version"`
	CallerID     string `json:"caller_id"`
	CalleeID     string `json:"callee_id"`
}

func callJSON(call *callsv1.Call) callResponse {
	return callResponse{CallID: call.GetCallId(), RoomID: call.GetRoomId(), State: call.GetState(),
		StateVersion: call.GetStateVersion(), CallerID: call.GetCallerId(), CalleeID: call.GetCalleeId()}
}

func (h *CallsHandler) identity(w http.ResponseWriter, r *http.Request) (*callsv1.Identity, bool) {
	identity, ok := middleware.IdentityFromContext(r.Context())
	if !ok || identity.Token == "" || identity.UserID == "" || identity.SessionID == "" {
		response.Error(w, http.StatusUnauthorized, "unauthenticated", "a session-bound access token is required")
		return nil, false
	}
	return &callsv1.Identity{AccessToken: identity.Token, UserId: identity.UserID, SessionId: identity.SessionID}, true
}

func (h *CallsHandler) backendError(w http.ResponseWriter, r *http.Request, err error) {
	if status.Code(err) == codes.Unavailable && status.Convert(err).Message() == "capacity" {
		w.Header().Set("Retry-After", "5")
	}
	code, tag, message := response.FromGRPC(err)
	if code >= 500 {
		h.logger.ErrorContext(r.Context(), "calls backend call failed", "error", err)
	}
	response.Error(w, code, tag, message)
}

func (h *CallsHandler) Create(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.identity(w, r)
	if !ok {
		return
	}
	var req struct {
		RoomID    string `json:"room_id"`
		CommandID string `json:"command_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		err.write(w)
		return
	}
	resp, err := h.client.Create(r.Context(), &callsv1.CreateRequest{Identity: identity, RoomId: req.RoomID, CommandId: req.CommandID})
	if err != nil {
		h.backendError(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, callJSON(resp.GetCall()))
}

func (h *CallsHandler) Accept(w http.ResponseWriter, r *http.Request) {
	identity, req, ok := h.commandRequest(w, r)
	if !ok {
		return
	}
	resp, err := h.client.Accept(r.Context(), &callsv1.AcceptRequest{Identity: identity, CallId: r.PathValue("call_id"), CommandId: req.CommandID})
	h.commandResponse(w, r, resp.GetCall(), err)
}

func (h *CallsHandler) Reject(w http.ResponseWriter, r *http.Request) {
	identity, req, ok := h.commandRequest(w, r)
	if !ok {
		return
	}
	resp, err := h.client.Reject(r.Context(), &callsv1.RejectRequest{Identity: identity, CallId: r.PathValue("call_id"), CommandId: req.CommandID})
	h.commandResponse(w, r, resp.GetCall(), err)
}

func (h *CallsHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	identity, req, ok := h.commandRequest(w, r)
	if !ok {
		return
	}
	resp, err := h.client.Cancel(r.Context(), &callsv1.CancelRequest{Identity: identity, CallId: r.PathValue("call_id"), CommandId: req.CommandID})
	h.commandResponse(w, r, resp.GetCall(), err)
}

func (h *CallsHandler) Connected(w http.ResponseWriter, r *http.Request) {
	identity, req, ok := h.commandRequest(w, r)
	if !ok {
		return
	}
	resp, err := h.client.Connected(r.Context(), &callsv1.ConnectedRequest{Identity: identity, CallId: r.PathValue("call_id"), CommandId: req.CommandID})
	h.commandResponse(w, r, resp.GetCall(), err)
}

func (h *CallsHandler) End(w http.ResponseWriter, r *http.Request) {
	identity, req, ok := h.commandRequest(w, r)
	if !ok {
		return
	}
	resp, err := h.client.Hangup(r.Context(), &callsv1.HangupRequest{Identity: identity, CallId: r.PathValue("call_id"), CommandId: req.CommandID})
	h.commandResponse(w, r, resp.GetCall(), err)
}

func (h *CallsHandler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	identity, req, ok := h.commandRequest(w, r)
	if !ok {
		return
	}
	resp, err := h.client.Heartbeat(r.Context(), &callsv1.HeartbeatRequest{Identity: identity, CallId: r.PathValue("call_id"), CommandId: req.CommandID})
	h.commandResponse(w, r, resp.GetCall(), err)
}

type callCommand struct {
	CommandID string `json:"command_id"`
}

func (h *CallsHandler) commandRequest(w http.ResponseWriter, r *http.Request) (*callsv1.Identity, callCommand, bool) {
	identity, ok := h.identity(w, r)
	if !ok {
		return nil, callCommand{}, false
	}
	var req callCommand
	if err := decodeJSON(r, &req); err != nil {
		err.write(w)
		return nil, callCommand{}, false
	}
	return identity, req, true
}

func (h *CallsHandler) commandResponse(w http.ResponseWriter, r *http.Request, call *callsv1.Call, err error) {
	if err != nil {
		h.backendError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, callJSON(call))
}

func (h *CallsHandler) Signal(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.identity(w, r)
	if !ok {
		return
	}
	var req struct {
		Kind      string `json:"kind"`
		Payload   string `json:"payload"`
		SenderSeq int64  `json:"sender_seq"`
	}
	if err := decodeJSON(r, &req); err != nil {
		err.write(w)
		return
	}
	resp, err := h.client.Signal(r.Context(), &callsv1.SignalRequest{Identity: identity, CallId: r.PathValue("call_id"),
		Kind: req.Kind, Payload: req.Payload, SenderSeq: req.SenderSeq})
	if err != nil {
		h.backendError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

func (h *CallsHandler) Get(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.identity(w, r)
	if !ok {
		return
	}
	resp, err := h.client.Get(r.Context(), &callsv1.GetRequest{Identity: identity, CallId: r.PathValue("call_id")})
	if err != nil {
		h.backendError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, callJSON(resp.GetCall()))
}

func (h *CallsHandler) Active(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.identity(w, r)
	if !ok {
		return
	}
	resp, err := h.client.ListActive(r.Context(), &callsv1.ListActiveRequest{Identity: identity})
	if err != nil {
		h.backendError(w, r, err)
		return
	}
	calls := resp.GetCalls()
	result := struct {
		Calls []callResponse `json:"calls"`
	}{Calls: make([]callResponse, 0, len(calls))}
	for _, call := range calls {
		result.Calls = append(result.Calls, callJSON(call))
	}
	response.JSON(w, http.StatusOK, result)
}

func (h *CallsHandler) Signals(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.identity(w, r)
	if !ok {
		return
	}
	after := int64(0)
	var err error
	if raw := r.URL.Query().Get("after"); raw != "" {
		after, err = strconv.ParseInt(raw, 10, 64)
	}
	if err != nil || after < 0 {
		response.Error(w, http.StatusBadRequest, "invalid_argument", "after must be a non-negative integer")
		return
	}
	resp, err := h.client.Signals(r.Context(), &callsv1.SignalsRequest{Identity: identity, CallId: r.PathValue("call_id"), After: after})
	if err != nil {
		h.backendError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, struct {
		Signals []*callsv1.Signal `json:"signals"`
	}{Signals: append([]*callsv1.Signal{}, resp.GetSignals()...)})
}

func (h *CallsHandler) Turn(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.identity(w, r)
	if !ok {
		return
	}
	var req struct{}
	if err := decodeJSON(r, &req); err != nil {
		err.write(w)
		return
	}
	resp, err := h.client.Turn(r.Context(), &callsv1.TurnRequest{Identity: identity, CallId: r.PathValue("call_id")})
	if err != nil {
		h.backendError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, resp)
}
