package integration

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/jwt"
)

// newAuthHandler wires the gRPC handler to the real service, repositories and
// token manager.
func newAuthHandler(t *testing.T) *handler.AuthGRPCHandler {
	t.Helper()

	pool := newTestPool(t)
	privatePEM, publicPEM := generateRSAKeys(t)
	tokens, err := jwt.NewManager(privatePEM, publicPEM, 15*time.Minute, time.Hour)
	require.NoError(t, err)

	svc, err := service.NewService(
		repository.NewUserRepository(pool),
		repository.NewSessionRepository(pool),
		service.NewArgon2idHasher(service.Argon2idParams{
			MemoryKiB: 1024, Time: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
		}),
		tokens,
		slog.Default(),
	)
	require.NoError(t, err)

	return handler.NewAuthGRPCHandler(svc)
}

func generateRSAKeys(t *testing.T) (privatePEM, publicPEM []byte) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)

	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
}

// registerAndLogin registers a new user and logs it in logins times.
func registerAndLogin(t *testing.T, h *handler.AuthGRPCHandler, logins int) []*authv1.LoginResponse {
	t.Helper()
	ctx := context.Background()

	email := "user-" + uuid.NewString() + "@example.com"
	const password = "correct-horse-42"
	_, err := h.Register(ctx, &authv1.RegisterRequest{
		Email: email, Username: "u" + uuid.NewString()[:8], Password: password,
	})
	require.NoError(t, err)

	resps := make([]*authv1.LoginResponse, logins)
	for i := range resps {
		resps[i], err = h.Login(ctx, &authv1.LoginRequest{Email: email, Password: password})
		require.NoError(t, err)
	}
	return resps
}

// requireActive checks whether the session of login can still be refreshed.
// A successful check rotates the session, so it is done once per login.
func requireActive(t *testing.T, h *handler.AuthGRPCHandler, login *authv1.LoginResponse, active bool) {
	t.Helper()

	_, err := h.RefreshToken(context.Background(),
		&authv1.RefreshTokenRequest{RefreshToken: login.GetRefreshToken()})
	if active {
		require.NoError(t, err)
	} else {
		require.Equal(t, codes.Unauthenticated, status.Code(err))
	}
}

func TestRevokeAllSessions_RevokesOnlyTheTokenOwnersSessions(t *testing.T) {
	h := newAuthHandler(t)
	ctx := context.Background()
	owner := registerAndLogin(t, h, 2)
	other := registerAndLogin(t, h, 1)

	resp, err := h.RevokeAllSessions(ctx,
		&authv1.RevokeAllSessionsRequest{AccessToken: owner[0].GetAccessToken()})
	require.NoError(t, err)
	require.EqualValues(t, 2, resp.GetRevokedCount())

	requireActive(t, h, owner[0], false)
	requireActive(t, h, owner[1], false)
	requireActive(t, h, other[0], true)
}

func TestRevokeAllSessions_RejectsUnauthenticatedRequests(t *testing.T) {
	h := newAuthHandler(t)
	ctx := context.Background()
	victim := registerAndLogin(t, h, 1)

	for name, token := range map[string]string{
		"no token":      "",
		"garbage token": "not-a-jwt",
		// A refresh token is not proof of an authenticated request.
		"refresh token": victim[0].GetRefreshToken(),
		// A token for the victim's id signed by a foreign key.
		"forged token": forgeAccessToken(t, victim[0].GetUserId()),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := h.RevokeAllSessions(ctx, &authv1.RevokeAllSessionsRequest{AccessToken: token})
			require.Equal(t, codes.Unauthenticated, status.Code(err))
		})
	}

	requireActive(t, h, victim[0], true)
}

// forgeAccessToken issues an access token for userID with a key the service
// does not trust.
func forgeAccessToken(t *testing.T, userID string) string {
	t.Helper()

	privatePEM, publicPEM := generateRSAKeys(t)
	tokens, err := jwt.NewManager(privatePEM, publicPEM, 15*time.Minute, time.Hour)
	require.NoError(t, err)
	token, _, err := tokens.IssueAccessToken(userID, "victim@example.com", "victim")
	require.NoError(t, err)
	return token
}
