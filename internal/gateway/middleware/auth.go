package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/response"
)

// TokenValidator is the part of the auth gRPC client the gateway needs to
// authenticate requests. Defined at the consumer per CONTRIBUTING.md;
// authv1.AuthServiceClient satisfies it.
type TokenValidator interface {
	ValidateToken(ctx context.Context, in *authv1.ValidateTokenRequest, opts ...grpc.CallOption) (*authv1.ValidateTokenResponse, error)
}

// Identity is the authenticated caller, as confirmed by the auth service.
type Identity struct {
	UserID    string
	Email     string
	Username  string
	ExpiresAt time.Time
	// Token is the access token the caller authenticated with, for backend
	// calls that authorize by token themselves (e.g. RevokeAllSessions).
	Token string
}

type identityKey struct{}

// WithIdentity returns a copy of ctx carrying identity.
func WithIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, identity)
}

// IdentityFromContext returns the identity stored by Authenticate.
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityKey{}).(Identity)
	return identity, ok
}

// Authenticate rejects requests without a valid access token and stores the
// caller's Identity in the request context. Tokens are verified by the auth
// service on every request (ARCHITECTURE.md section 3.1), so revocation there
// takes effect at the gateway immediately.
func Authenticate(validator TokenValidator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r.Header.Get("Authorization"))
			if !ok {
				w.Header().Set("WWW-Authenticate", "Bearer")
				response.Error(w, http.StatusUnauthorized, "unauthenticated", "missing or malformed bearer token")
				return
			}

			resp, err := validator.ValidateToken(r.Context(), &authv1.ValidateTokenRequest{Token: token})
			if err != nil {
				response.GRPCError(w, err)
				return
			}
			if !resp.GetValid() {
				w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
				response.Error(w, http.StatusUnauthorized, "unauthenticated", "invalid or expired token")
				return
			}

			identity := Identity{
				UserID:   resp.GetUserId(),
				Email:    resp.GetEmail(),
				Username: resp.GetUsername(),
				Token:    token,
			}
			if expiresAt := resp.GetExpiresAt(); expiresAt != nil {
				identity.ExpiresAt = expiresAt.AsTime()
			}
			next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), identity)))
		})
	}
}

// bearerToken extracts the credential from an "Authorization: Bearer <token>"
// header (RFC 6750). The scheme is case-insensitive; the token must be a
// single run of visible ASCII characters.
func bearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}

	token := header[len(prefix):]
	for i := 0; i < len(token); i++ {
		if token[i] <= ' ' || token[i] >= 0x7f {
			return "", false
		}
	}
	return token, true
}
