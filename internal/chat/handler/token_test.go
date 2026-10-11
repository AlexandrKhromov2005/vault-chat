package handler

import (
	"context"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"strings"
	"testing"
)

func TestBearerCredential(t *testing.T) {
	for _, input := range []string{"Bearer abc", "bearer abc"} {
		token, ok := bearerCredential(metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", input)))
		require.True(t, ok)
		require.Equal(t, "abc", token)
	}
	for _, values := range [][]string{nil, {"Basic abc"}, {"Bearer "}, {"Bearer a b"}, {"Bearer a\n"}, {"Bearer abc", "Bearer xyz"}} {
		token, ok := bearerCredential(metadata.NewIncomingContext(context.Background(), metadata.MD{"authorization": values}))
		require.False(t, ok)
		require.Empty(t, token)
	}
}

func FuzzBearerCredential(f *testing.F) {
	for _, input := range []string{"", "Bearer abc", "bearer abc", "Bearer a b", "Bearer a\x00"} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		token, ok := bearerCredential(metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", input)))
		if !ok {
			require.Empty(t, token)
			return
		}
		require.True(t, strings.EqualFold(input[:7], "Bearer "))
		require.NotEmpty(t, token)
		for i := 0; i < len(token); i++ {
			require.Greater(t, token[i], byte(' '))
			require.Less(t, token[i], byte(0x7f))
		}
	})
}
