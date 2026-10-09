// Package response writes the JSON responses of the API gateway and maps
// backend gRPC errors to HTTP.
package response

import (
	"encoding/json"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrorBody is the envelope of every error response:
// {"error":{"code":"...","message":"..."}}.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail is a machine-readable code plus a human-readable message.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// JSON writes body as a JSON response with the given status code.
func JSON(w http.ResponseWriter, statusCode int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	// The status line is already sent, so an encoding error (a failed write,
	// or a value JSON cannot represent) can no longer be reported to the
	// client. The gateway's response types contain only encodable fields.
	_ = json.NewEncoder(w).Encode(body)
}

// Error writes an error response in the ErrorBody format.
func Error(w http.ResponseWriter, statusCode int, code, message string) {
	JSON(w, statusCode, ErrorBody{Error: ErrorDetail{Code: code, Message: message}})
}

// GRPCError writes the HTTP equivalent of a backend gRPC error.
func GRPCError(w http.ResponseWriter, err error) {
	statusCode, code, message := FromGRPC(err)
	Error(w, statusCode, code, message)
}

// FromGRPC maps a backend gRPC error to an HTTP status, an error code, and a
// client-safe message. Messages of client errors (4xx) are passed through;
// server failures (5xx) get a fixed message so that upstream internals such
// as addresses or SQL errors never reach clients.
func FromGRPC(err error) (statusCode int, code, message string) {
	st, ok := status.FromError(err)
	if !ok {
		st = status.FromContextError(err)
	}

	switch st.Code() {
	case codes.InvalidArgument:
		return http.StatusBadRequest, "invalid_argument", st.Message()
	case codes.Unauthenticated:
		return http.StatusUnauthorized, "unauthenticated", st.Message()
	case codes.PermissionDenied:
		return http.StatusForbidden, "permission_denied", st.Message()
	case codes.NotFound:
		return http.StatusNotFound, "not_found", st.Message()
	case codes.AlreadyExists:
		return http.StatusConflict, "already_exists", st.Message()
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests, "rate_limited", st.Message()
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout, "timeout", "upstream service timed out"
	case codes.Unavailable, codes.Canceled:
		return http.StatusServiceUnavailable, "unavailable", "upstream service unavailable"
	default:
		return http.StatusInternalServerError, "internal", "internal error"
	}
}
