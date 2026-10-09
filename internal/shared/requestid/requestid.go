// Package requestid propagates the correlation ID of a request across HTTP
// and gRPC hops (ARCHITECTURE.md section 7.1).
package requestid

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	// Header is the HTTP header carrying the request id.
	Header = "X-Request-ID"
	// MetadataKey is the gRPC metadata key carrying the request id.
	MetadataKey = "x-request-id"

	maxLength = 64
)

type contextKey struct{}

// New returns a fresh random request id.
func New() string {
	return uuid.NewString()
}

// Valid reports whether id is safe to accept from a client: 1-64 ASCII
// letters, digits, dashes, underscores, or dots. Anything else could smuggle
// data into headers or log lines and must be replaced with New.
func Valid(id string) bool {
	if len(id) == 0 || len(id) > maxLength {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' {
			continue
		}
		return false
	}
	return true
}

// NewContext returns a copy of ctx carrying id.
func NewContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// FromContext returns the request id stored in ctx, or "" if there is none.
func FromContext(ctx context.Context) string {
	id, _ := ctx.Value(contextKey{}).(string)
	return id
}

// UnaryServerInterceptor stores the caller's request id in the handler's
// context, so that logs written with that context carry it. A missing or
// unsafe id is replaced with a fresh one, as RequestID does for HTTP.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		var id string
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if values := md.Get(MetadataKey); len(values) > 0 {
				id = values[0]
			}
		}
		if !Valid(id) {
			id = New()
		}
		return handler(NewContext(ctx, id), req)
	}
}

// UnaryClientInterceptor forwards the request id from the context to the
// callee as gRPC metadata.
func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if id := FromContext(ctx); id != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, MetadataKey, id)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
