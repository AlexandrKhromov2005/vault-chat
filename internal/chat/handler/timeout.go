package handler

import (
	"context"
	"time"

	"google.golang.org/grpc"
)

// UnaryTimeout bounds all unary server work, including Auth calls and storage.
// WithTimeout preserves an earlier deadline supplied by the caller.
func UnaryTimeout(budget time.Duration) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		ctx, cancel := context.WithTimeout(ctx, budget)
		defer cancel()
		return next(ctx, req)
	}
}
