package jwt_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"
	"unicode/utf8"

	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/jwt"
)

// maxFuzzTokenLength bounds fuzzed tokens; real tokens are well below 2KB.
const maxFuzzTokenLength = 8 << 10

// maxFuzzClaimLength bounds each fuzzed identity field so that a short
// local run explores many inputs.
const maxFuzzClaimLength = 256

// maxFuzzPEMLength bounds fuzzed PEM inputs; a 4096-bit key is about 3.3KB.
const maxFuzzPEMLength = 16 << 10

func newFuzzManager(f *testing.F) (*jwt.Manager, []byte) {
	f.Helper()

	privatePEM, publicPEM := generateTestKeys(f)
	manager, err := jwt.NewManager(privatePEM, publicPEM, 15*time.Minute, time.Hour)
	require.NoError(f, err)
	return manager, privatePEM
}

// signRaw signs claims with method and key, bypassing Manager, to seed tokens
// that Manager must reject.
func signRaw(f *testing.F, method jwtv5.SigningMethod, key any, claims jwtv5.MapClaims) string {
	f.Helper()

	token, err := jwtv5.NewWithClaims(method, claims).SignedString(key)
	require.NoError(f, err)
	return token
}

// FuzzManagerValidate verifies that validating an arbitrary string never
// panics and that only tokens issued by the manager are accepted, with the
// claims they were issued with.
func FuzzManagerValidate(f *testing.F) {
	manager, privatePEM := newFuzzManager(f)
	privateKey, err := jwtv5.ParseRSAPrivateKeyFromPEM(privatePEM)
	require.NoError(f, err)

	// issued maps the jti of every token the manager issued to its claims.
	issued := map[string]*jwt.Claims{}
	issue := func(token string, _ time.Time, err error) string {
		require.NoError(f, err)
		claims, err := manager.Validate(token)
		require.NoError(f, err)
		issued[claims.ID] = claims
		return token
	}
	access := issue(manager.IssueAccessToken("user-1", "user@example.com", "user1"))
	refresh := issue(manager.IssueRefreshToken(uuid.NewString(), "user-1", "user@example.com", "user1"))

	foreign, _ := newFuzzManager(f)
	foreignToken, _, err := foreign.IssueAccessToken("user-1", "user@example.com", "user1")
	require.NoError(f, err)

	valid := jwtv5.MapClaims{
		"iss":        "vault-chat-auth",
		"sub":        "user-1",
		"jti":        uuid.NewString(),
		"exp":        time.Now().Add(time.Hour).Unix(),
		"token_type": "access",
	}
	withClaim := func(key string, value any) jwtv5.MapClaims {
		claims := jwtv5.MapClaims{}
		for k, v := range valid {
			claims[k] = v
		}
		if value == nil {
			delete(claims, key)
		} else {
			claims[key] = value
		}
		return claims
	}

	seeds := []string{
		access,
		refresh,
		"",
		"not-a-jwt",
		"a.b.c",
		"..",
		access[:len(access)/2],
		access[:len(access)-10] + "AAAAAAAAAA",
		access + ".extra",
		foreignToken,
		signRaw(f, jwtv5.SigningMethodHS256, []byte("secret"), valid),
		signRaw(f, jwtv5.SigningMethodNone, jwtv5.UnsafeAllowNoneSignatureType, valid),
		signRaw(f, jwtv5.SigningMethodPS256, privateKey, valid),
		signRaw(f, jwtv5.SigningMethodRS256, privateKey, withClaim("exp", time.Now().Add(-time.Hour).Unix())),
		signRaw(f, jwtv5.SigningMethodRS256, privateKey, withClaim("exp", nil)),
		signRaw(f, jwtv5.SigningMethodRS256, privateKey, withClaim("iss", "someone-else")),
		signRaw(f, jwtv5.SigningMethodRS256, privateKey, withClaim("exp", "tomorrow")),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, token string) {
		if len(token) > maxFuzzTokenLength {
			t.Skip("token too large")
		}

		claims, err := manager.Validate(token)
		if err != nil {
			require.Nil(t, claims)
			return
		}

		// Only the manager can sign accepted tokens, so an accepted token
		// must carry exactly the claims of a token it issued.
		want, ok := issued[claims.ID]
		require.True(t, ok, "accepted a token the manager did not issue: %q", token)
		require.Equal(t, want, claims)
		require.True(t, claims.ExpiresAt.After(time.Now()), "accepted an expired token")
	})
}

// FuzzRefreshTokenRoundTrip verifies that a refresh token keeps the session
// id, identity and type it was issued with.
func FuzzRefreshTokenRoundTrip(f *testing.F) {
	manager, _ := newFuzzManager(f)

	f.Add(uuid.NewString(), uuid.NewString(), "user@example.com", "user1")
	f.Add("", "", "", "")
	f.Add("session", "user", "Ünïcödé@example.com", "имя")
	f.Add(`"quoted"`, `\back\slash`, "<tag>&", "\x00\x01\x7f")

	f.Fuzz(func(t *testing.T, sessionID, userID, email, username string) {
		for _, s := range []string{sessionID, userID, email, username} {
			if len(s) > maxFuzzClaimLength {
				t.Skip("claim too large")
			}
			// JSON cannot carry invalid UTF-8; issued identities are
			// validated UUIDs, emails and usernames.
			if !utf8.ValidString(s) {
				t.Skip("invalid UTF-8")
			}
		}

		token, expiresAt, err := manager.IssueRefreshToken(sessionID, userID, email, username)
		require.NoError(t, err)

		claims, err := manager.Validate(token)
		require.NoError(t, err)
		require.Equal(t, sessionID, claims.ID)
		require.Equal(t, userID, claims.UserID)
		require.Equal(t, email, claims.Email)
		require.Equal(t, username, claims.Username)
		require.Equal(t, jwt.RefreshToken, claims.TokenType)
		require.WithinDuration(t, expiresAt, claims.ExpiresAt, time.Second)
	})
}

// pemSeeds returns PEM inputs for the key parsers: every supported encoding of
// the test key, a non-RSA key and malformed data.
func pemSeeds(f *testing.F) [][]byte {
	f.Helper()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(f, err)
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(f, err)

	mustDER := must(f)
	encode := func(typ string, der []byte) []byte {
		return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
	}
	pkcs8 := encode("PRIVATE KEY", mustDER(x509.MarshalPKCS8PrivateKey(rsaKey)))

	return [][]byte{
		pkcs8,
		encode("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(rsaKey)),
		encode("PUBLIC KEY", mustDER(x509.MarshalPKIXPublicKey(&rsaKey.PublicKey))),
		encode("RSA PUBLIC KEY", x509.MarshalPKCS1PublicKey(&rsaKey.PublicKey)),
		encode("PRIVATE KEY", mustDER(x509.MarshalPKCS8PrivateKey(ecKey))),
		encode("PUBLIC KEY", mustDER(x509.MarshalPKIXPublicKey(&ecKey.PublicKey))),
		pkcs8[:len(pkcs8)/2],
		encode("PRIVATE KEY", []byte("garbage")),
		[]byte("not pem"),
		nil,
	}
}

// must returns a helper that fails tb if marshaling a key failed.
func must(tb testing.TB) func(der []byte, err error) []byte {
	return func(der []byte, err error) []byte {
		require.NoError(tb, err)
		return der
	}
}

// FuzzNewManager_PrivateKey verifies that parsing an arbitrary private key
// never panics and accepts exactly the RSA keys that the reference parser of
// golang-jwt accepts.
func FuzzNewManager_PrivateKey(f *testing.F) {
	_, publicPEM := generateTestKeys(f)
	for _, seed := range pemSeeds(f) {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, privatePEM []byte) {
		if len(privatePEM) > maxFuzzPEMLength {
			t.Skip("input too large")
		}

		manager, err := jwt.NewManager(privatePEM, publicPEM, time.Minute, time.Hour)
		_, refErr := jwtv5.ParseRSAPrivateKeyFromPEM(privatePEM)
		if err != nil {
			require.Nil(t, manager)
		}
		require.Equal(t, refErr == nil, err == nil,
			"accepted a key the reference parser rejects or vice versa: err=%v, reference err=%v", err, refErr)
	})
}

// FuzzNewManager_PublicKey verifies that parsing an arbitrary public key never
// panics and that only the matching public key validates the manager's tokens.
func FuzzNewManager_PublicKey(f *testing.F) {
	manager, privatePEM := newFuzzManager(f)
	token, _, err := manager.IssueAccessToken("user-1", "user@example.com", "user1")
	require.NoError(f, err)
	privateKey, err := jwtv5.ParseRSAPrivateKeyFromPEM(privatePEM)
	require.NoError(f, err)

	f.Add(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: must(f)(x509.MarshalPKIXPublicKey(&privateKey.PublicKey)),
	}))
	for _, seed := range pemSeeds(f) {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, publicPEM []byte) {
		if len(publicPEM) > maxFuzzPEMLength {
			t.Skip("input too large")
		}

		candidate, err := jwt.NewManager(privatePEM, publicPEM, time.Minute, time.Hour)
		if err != nil {
			require.Nil(t, candidate)
			return
		}

		// The token is signed with privatePEM, so only its public key may
		// validate it.
		if _, err := candidate.Validate(token); err == nil {
			block, _ := pem.Decode(publicPEM)
			got, err := x509.ParsePKIXPublicKey(block.Bytes)
			if err != nil {
				got, err = x509.ParsePKCS1PublicKey(block.Bytes)
			}
			require.NoError(t, err)
			require.True(t, privateKey.PublicKey.Equal(got), "a foreign public key validated the token")
		}
	})
}
