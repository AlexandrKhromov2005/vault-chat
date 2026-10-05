// Package jwt provides RS256 JSON Web Token issuance and validation shared
// by Vault Chat services.
package jwt

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// issuer identifies tokens minted by the auth service.
const issuer = "vault-chat-auth"

// TokenType distinguishes access tokens from refresh tokens.
type TokenType string

const (
	// AccessToken authorizes API requests; it is short-lived.
	AccessToken TokenType = "access"
	// RefreshToken obtains new token pairs; it is long-lived.
	RefreshToken TokenType = "refresh"
)

// Claims is the validated identity carried by a token.
type Claims struct {
	UserID    string
	Email     string
	Username  string
	TokenType TokenType
	ExpiresAt time.Time
}

// Manager issues and validates RS256-signed JWTs.
type Manager struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
	accessTTL  time.Duration
	refreshTTL time.Duration
}

// NewManager builds a Manager from PEM-encoded RSA keys. The private key may
// be PKCS#1 or PKCS#8; the public key may be PKIX or PKCS#1.
func NewManager(privatePEM, publicPEM []byte, accessTTL, refreshTTL time.Duration) (*Manager, error) {
	privateKey, err := parsePrivateKey(privatePEM)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	publicKey, err := parsePublicKey(publicPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	if accessTTL <= 0 || refreshTTL <= 0 {
		return nil, errors.New("token TTLs must be positive")
	}

	return &Manager{
		privateKey: privateKey,
		publicKey:  publicKey,
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
	}, nil
}

// IssueAccessToken mints a short-lived access token for the given identity.
func (m *Manager) IssueAccessToken(userID, email, username string) (string, time.Time, error) {
	return m.issue(userID, email, username, AccessToken, m.accessTTL)
}

// IssueRefreshToken mints a long-lived refresh token for the given identity.
func (m *Manager) IssueRefreshToken(userID, email, username string) (string, time.Time, error) {
	return m.issue(userID, email, username, RefreshToken, m.refreshTTL)
}

// Validate verifies the signature, expiry, and issuer of tokenString and
// returns the embedded identity.
func (m *Manager) Validate(tokenString string) (*Claims, error) {
	var claims tokenClaims

	_, err := jwtv5.ParseWithClaims(tokenString, &claims, func(token *jwtv5.Token) (any, error) {
		if _, ok := token.Method.(*jwtv5.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.publicKey, nil
	}, jwtv5.WithIssuer(issuer), jwtv5.WithExpirationRequired())
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	return &Claims{
		UserID:    claims.Subject,
		Email:     claims.Email,
		Username:  claims.Username,
		TokenType: TokenType(claims.TokenType),
		ExpiresAt: claims.ExpiresAt.Time,
	}, nil
}

// tokenClaims is the on-the-wire JWT representation.
type tokenClaims struct {
	jwtv5.RegisteredClaims
	TokenType string `json:"token_type"`
	Email     string `json:"email"`
	Username  string `json:"username"`
}

func (m *Manager) issue(userID, email, username string, tokenType TokenType, ttl time.Duration) (string, time.Time, error) {
	now := time.Now()
	expiresAt := now.Add(ttl)

	claims := tokenClaims{
		RegisteredClaims: jwtv5.RegisteredClaims{
			ID:        uuid.NewString(),
			Subject:   userID,
			Issuer:    issuer,
			IssuedAt:  jwtv5.NewNumericDate(now),
			ExpiresAt: jwtv5.NewNumericDate(expiresAt),
		},
		TokenType: string(tokenType),
		Email:     email,
		Username:  username,
	}

	token := jwtv5.NewWithClaims(jwtv5.SigningMethodRS256, claims)
	signed, err := token.SignedString(m.privateKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to sign token: %w", err)
	}
	return signed, expiresAt, nil
}

func parsePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no PEM block found")
	}

	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
		return nil, errors.New("private key is not RSA")
	}

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("unsupported private key format: %w", err)
	}
	return key, nil
}

func parsePublicKey(data []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no PEM block found")
	}

	if key, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PublicKey); ok {
			return rsaKey, nil
		}
		return nil, errors.New("public key is not RSA")
	}

	key, err := x509.ParsePKCS1PublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("unsupported public key format: %w", err)
	}
	return key, nil
}
