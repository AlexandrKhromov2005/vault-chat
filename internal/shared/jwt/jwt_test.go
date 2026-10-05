package jwt_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/jwt"
)

func generateTestKeys(t *testing.T) (privatePEM, publicPEM []byte) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	privatePEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})

	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	publicPEM = pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})

	return privatePEM, publicPEM
}

func TestNewManager(t *testing.T) {
	privatePEM, publicPEM := generateTestKeys(t)

	tests := []struct {
		name       string
		privatePEM []byte
		publicPEM  []byte
		wantErr    bool
	}{
		{"valid key pair", privatePEM, publicPEM, false},
		{"garbage private key", []byte("not pem"), publicPEM, true},
		{"garbage public key", privatePEM, []byte("not pem"), true},
		{"empty private key", nil, publicPEM, true},
		{"empty public key", privatePEM, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := jwt.NewManager(tt.privatePEM, tt.publicPEM, time.Minute, time.Hour)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestManager_IssueAndValidate(t *testing.T) {
	privatePEM, publicPEM := generateTestKeys(t)
	manager, err := jwt.NewManager(privatePEM, publicPEM, 15*time.Minute, 720*time.Hour)
	require.NoError(t, err)

	t.Run("access token round-trip", func(t *testing.T) {
		token, expiresAt, err := manager.IssueAccessToken("user-1", "user@example.com", "user1")
		require.NoError(t, err)
		require.NotEmpty(t, token)
		require.WithinDuration(t, time.Now().Add(15*time.Minute), expiresAt, 5*time.Second)

		claims, err := manager.Validate(token)
		require.NoError(t, err)
		require.Equal(t, "user-1", claims.UserID)
		require.Equal(t, "user@example.com", claims.Email)
		require.Equal(t, "user1", claims.Username)
		require.Equal(t, jwt.AccessToken, claims.TokenType)
	})

	t.Run("refresh token round-trip", func(t *testing.T) {
		token, expiresAt, err := manager.IssueRefreshToken("session-2", "user-2", "u2@example.com", "user2")
		require.NoError(t, err)
		require.WithinDuration(t, time.Now().Add(720*time.Hour), expiresAt, 5*time.Second)

		claims, err := manager.Validate(token)
		require.NoError(t, err)
		require.Equal(t, jwt.RefreshToken, claims.TokenType)
		require.Equal(t, "user-2", claims.UserID)
		require.Equal(t, "session-2", claims.ID, "refresh token jti must be the session id")
	})

	t.Run("access tokens get unique ids", func(t *testing.T) {
		first, _, err := manager.IssueAccessToken("user-1", "user@example.com", "user1")
		require.NoError(t, err)
		second, _, err := manager.IssueAccessToken("user-1", "user@example.com", "user1")
		require.NoError(t, err)

		firstClaims, err := manager.Validate(first)
		require.NoError(t, err)
		secondClaims, err := manager.Validate(second)
		require.NoError(t, err)
		require.NotEmpty(t, firstClaims.ID)
		require.NotEqual(t, firstClaims.ID, secondClaims.ID)
	})
}

func TestManager_Validate(t *testing.T) {
	privatePEM, publicPEM := generateTestKeys(t)
	manager, err := jwt.NewManager(privatePEM, publicPEM, 15*time.Minute, 720*time.Hour)
	require.NoError(t, err)

	validToken, _, err := manager.IssueAccessToken("user-1", "user@example.com", "user1")
	require.NoError(t, err)

	privateKey, err := jwtv5.ParseRSAPrivateKeyFromPEM(privatePEM)
	require.NoError(t, err)
	expiredToken, err := jwtv5.NewWithClaims(jwtv5.SigningMethodRS256, jwtv5.MapClaims{
		"iss":        "vault-chat-auth",
		"sub":        "user-1",
		"iat":        time.Now().Add(-2 * time.Hour).Unix(),
		"exp":        time.Now().Add(-time.Hour).Unix(),
		"token_type": "access",
	}).SignedString(privateKey)
	require.NoError(t, err)

	otherPrivatePEM, _ := generateTestKeys(t)
	otherManager, err := jwt.NewManager(otherPrivatePEM, publicPEM, 15*time.Minute, time.Hour)
	require.NoError(t, err)
	foreignToken, _, err := otherManager.IssueAccessToken("user-1", "user@example.com", "user1")
	require.NoError(t, err)

	hmacToken, err := jwtv5.NewWithClaims(jwtv5.SigningMethodHS256, jwtv5.MapClaims{
		"sub": "user-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte("secret"))
	require.NoError(t, err)

	tests := []struct {
		name  string
		token string
	}{
		{"valid baseline is covered elsewhere", validToken},
		{"garbage", "not-a-jwt"},
		{"empty", ""},
		{"expired", expiredToken},
		{"signed with another key", foreignToken},
		{"wrong algorithm", hmacToken},
		{"tampered payload", validToken[:len(validToken)-10] + "AAAAAAAAAA"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := manager.Validate(tt.token)
			if tt.name == "valid baseline is covered elsewhere" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
		})
	}
}
