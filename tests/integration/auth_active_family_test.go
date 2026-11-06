package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
)

// The login id that ValidateToken returns is the session family: the id of
// the login's first session, the same after refresh rotation, different for
// another login, and gone once the login is revoked.
func TestActiveFamilyIdentifiesTheLoginAcrossRotation(t *testing.T) {
	pool, _, user := emailFixture(t, true)
	ctx := context.Background()
	sessions := repository.NewSessionRepository(pool)

	first := newTestSession(user.ID, time.Hour)
	require.NoError(t, sessions.Create(ctx, first))
	rotated := newTestSession(user.ID, time.Hour)
	require.NoError(t, rotate(ctx, sessions, first.ID, rotated))
	other := newTestSession(user.ID, time.Hour)
	require.NoError(t, sessions.Create(ctx, other))

	for _, sid := range []string{first.ID, rotated.ID} {
		login, err := sessions.ActiveFamily(ctx, user.ID, sid)
		require.NoError(t, err)
		require.Equal(t, first.ID, login, "session %s", sid)
	}
	login, err := sessions.ActiveFamily(ctx, user.ID, other.ID)
	require.NoError(t, err)
	require.Equal(t, other.ID, login)

	login, err = sessions.ActiveFamily(ctx, uuid.NewString(), rotated.ID)
	require.NoError(t, err)
	require.Empty(t, login, "another user's session")

	require.NoError(t, sessions.RevokeFamily(ctx, user.ID, rotated.ID))
	login, err = sessions.ActiveFamily(ctx, user.ID, rotated.ID)
	require.NoError(t, err)
	require.Empty(t, login, "revoked login")
	login, err = sessions.ActiveFamily(ctx, user.ID, other.ID)
	require.NoError(t, err)
	require.Equal(t, other.ID, login, "revoking one login leaves the other")
}
