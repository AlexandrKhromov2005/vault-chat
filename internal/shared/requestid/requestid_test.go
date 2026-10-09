package requestid_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/requestid"
)

func TestValid(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{"uuid", "3f1c2d4e-5a6b-4c7d-8e9f-0a1b2c3d4e5f", true},
		{"dots and underscores", "req_1.2", true},
		{"max length", strings.Repeat("a", 64), true},
		{"empty", "", false},
		{"too long", strings.Repeat("a", 65), false},
		{"space", "req 1", false},
		{"newline injection", "req\nX-Admin: 1", false},
		{"non-ascii", "запрос", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, requestid.Valid(tt.id))
		})
	}
}

func TestNew(t *testing.T) {
	first, second := requestid.New(), requestid.New()
	require.True(t, requestid.Valid(first))
	require.NotEqual(t, first, second)
}

func TestContext(t *testing.T) {
	require.Empty(t, requestid.FromContext(context.Background()))

	ctx := requestid.NewContext(context.Background(), "req-1")
	require.Equal(t, "req-1", requestid.FromContext(ctx))
}

func TestUnaryServerInterceptor(t *testing.T) {
	interceptor := requestid.UnaryServerInterceptor()

	handle := func(md metadata.MD) string {
		ctx := context.Background()
		if md != nil {
			ctx = metadata.NewIncomingContext(ctx, md)
		}
		var got string
		_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/auth.v1.AuthService/Login"},
			func(ctx context.Context, _ any) (any, error) {
				got = requestid.FromContext(ctx)
				return nil, nil
			})
		require.NoError(t, err)
		return got
	}

	t.Run("adopts the caller's request id", func(t *testing.T) {
		require.Equal(t, "req-1", handle(metadata.Pairs(requestid.MetadataKey, "req-1")))
	})

	t.Run("replaces an unsafe request id", func(t *testing.T) {
		got := handle(metadata.Pairs(requestid.MetadataKey, "bad id\nX: 1"))
		require.NotEqual(t, "bad id\nX: 1", got)
		require.True(t, requestid.Valid(got))
	})

	t.Run("generates one when the caller sent none", func(t *testing.T) {
		require.True(t, requestid.Valid(handle(nil)))
	})
}

func TestUnaryClientInterceptor(t *testing.T) {
	interceptor := requestid.UnaryClientInterceptor()

	invoke := func(ctx context.Context) metadata.MD {
		var got metadata.MD
		invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			got, _ = metadata.FromOutgoingContext(ctx)
			return nil
		}
		require.NoError(t, interceptor(ctx, "/auth.v1.AuthService/Login", nil, nil, nil, invoker))
		return got
	}

	t.Run("propagates request id", func(t *testing.T) {
		md := invoke(requestid.NewContext(context.Background(), "req-1"))
		require.Equal(t, []string{"req-1"}, md.Get(requestid.MetadataKey))
	})

	t.Run("no request id leaves metadata untouched", func(t *testing.T) {
		md := invoke(context.Background())
		require.Empty(t, md.Get(requestid.MetadataKey))
	})
}
