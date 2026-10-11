package handler_test

import (
	"context"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/handler"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"testing"
	"time"
)

func TestUnaryTimeout_RespectsServerAndCallerDeadlines(t *testing.T) {
	for _, callerBudget := range []time.Duration{time.Second, 10 * time.Millisecond} {
		ctx, cancel := context.WithTimeout(context.Background(), callerBudget)
		defer cancel()
		parentDeadline, _ := ctx.Deadline()
		start := time.Now()
		_, err := handler.UnaryTimeout(40*time.Millisecond)(ctx, nil, &grpc.UnaryServerInfo{}, func(ctx context.Context, _ any) (any, error) {
			deadline, ok := ctx.Deadline()
			require.True(t, ok)
			require.False(t, deadline.After(parentDeadline))
			<-ctx.Done()
			return nil, ctx.Err()
		})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Less(t, time.Since(start), 300*time.Millisecond)
	}
}
