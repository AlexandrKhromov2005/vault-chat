package integration

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
)

// A client whose refresh response was lost retries with the same token. The
// retry must not be mistaken for token theft: it succeeds, the login goes on
// and the other logins of the user are untouched.
func TestRefreshToken_RetryAfterLostResponse(t *testing.T) {
	h := newAuthHandler(t)
	ctx := context.Background()
	logins := registerAndLogin(t, h, 2)
	request := &authv1.RefreshTokenRequest{RefreshToken: logins[0].GetRefreshToken()}

	_, err := h.RefreshToken(ctx, request) // the response never reaches the client
	require.NoError(t, err)

	retried, err := h.RefreshToken(ctx, request)
	require.NoError(t, err, "a prompt retry with the same token must succeed")

	requireValidAccess(t, h, retried.GetAccessToken(), true)
	_, err = h.RefreshToken(ctx, &authv1.RefreshTokenRequest{RefreshToken: retried.GetRefreshToken()})
	require.NoError(t, err, "the login must continue with the tokens returned to the retry")
	requireActive(t, h, logins[1], true)
}

// Simultaneous refreshes with one token, e.g. from two requests of the same
// client, all succeed and continue one login.
func TestRefreshToken_ConcurrentRefreshesWithOneToken(t *testing.T) {
	h := newAuthHandler(t)
	ctx := context.Background()
	logins := registerAndLogin(t, h, 2)
	request := &authv1.RefreshTokenRequest{RefreshToken: logins[0].GetRefreshToken()}

	const attempts = 8
	resps := make([]*authv1.RefreshTokenResponse, attempts)
	errs := make([]error, attempts)
	var wg sync.WaitGroup
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resps[i], errs[i] = h.RefreshToken(ctx, request)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}

	// Every pair is bound to the one successor session: after one of them
	// moves on, another is still a retry of the same rotation.
	_, err := h.RefreshToken(ctx, &authv1.RefreshTokenRequest{RefreshToken: resps[0].GetRefreshToken()})
	require.NoError(t, err)
	_, err = h.RefreshToken(ctx, &authv1.RefreshTokenRequest{RefreshToken: resps[attempts-1].GetRefreshToken()})
	require.NoError(t, err, "a pair of the same successor is retried within the window")
	requireActive(t, h, logins[1], true)
}

// Theft detection still applies to a rotated token that is not a retry.
func TestRefreshToken_ReuseIsStillDetected(t *testing.T) {
	cases := map[string]struct {
		options []service.Option
		// continueLogin is what the legitimate client does with the
		// response to the first refresh before the old token is replayed.
		continueLogin func(t *testing.T, h *handler.AuthGRPCHandler, rotated *authv1.RefreshTokenResponse)
	}{
		"replay after the client used the new token": {
			continueLogin: func(t *testing.T, h *handler.AuthGRPCHandler, rotated *authv1.RefreshTokenResponse) {
				_, err := h.RefreshToken(context.Background(),
					&authv1.RefreshTokenRequest{RefreshToken: rotated.GetRefreshToken()})
				require.NoError(t, err)
			},
		},
		"replay with retries disabled": {
			options:       []service.Option{service.WithRefreshRetryWindow(0)},
			continueLogin: func(*testing.T, *handler.AuthGRPCHandler, *authv1.RefreshTokenResponse) {},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newAuthHandler(t, tc.options...)
			ctx := context.Background()
			logins := registerAndLogin(t, h, 2)
			stolen := &authv1.RefreshTokenRequest{RefreshToken: logins[0].GetRefreshToken()}

			rotated, err := h.RefreshToken(ctx, stolen)
			require.NoError(t, err)
			tc.continueLogin(t, h, rotated)

			_, err = h.RefreshToken(ctx, stolen)
			require.Equal(t, codes.Unauthenticated, status.Code(err))
			requireValidAccess(t, h, rotated.GetAccessToken(), false)
			requireActive(t, h, logins[1], false)
		})
	}
}

// A retry cannot bring back a login that was logged out in the meantime.
func TestRefreshToken_RetryAfterLogoutIsRejected(t *testing.T) {
	h := newAuthHandler(t)
	ctx := context.Background()
	logins := registerAndLogin(t, h, 1)
	request := &authv1.RefreshTokenRequest{RefreshToken: logins[0].GetRefreshToken()}

	rotated, err := h.RefreshToken(ctx, request)
	require.NoError(t, err)
	_, err = h.Logout(ctx, &authv1.LogoutRequest{RefreshToken: rotated.GetRefreshToken()})
	require.NoError(t, err)

	_, err = h.RefreshToken(ctx, request)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	requireValidAccess(t, h, rotated.GetAccessToken(), false)
}

func requireValidAccess(t *testing.T, h *handler.AuthGRPCHandler, token string, valid bool) {
	t.Helper()

	resp, err := h.ValidateToken(context.Background(), &authv1.ValidateTokenRequest{Token: token})
	require.NoError(t, err)
	require.Equal(t, valid, resp.GetValid())
}
