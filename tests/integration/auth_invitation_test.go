package integration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
)

// testInvitation performs the operator action for test registrations, using
// the disposable Auth database. No public API can issue invitations.
func testInvitation(t *testing.T, email string) string {
	t.Helper()
	secret := make([]byte, 32)
	_, err := rand.Read(secret)
	require.NoError(t, err)
	token := hex.EncodeToString(secret)
	hash := sha256.Sum256([]byte(token))
	conn, err := pgx.Connect(context.Background(), os.Getenv("VAULT_CHAT_TEST_DATABASE_URL"))
	require.NoError(t, err)
	defer conn.Close(context.Background())
	_, err = conn.Exec(context.Background(), `INSERT INTO registration_invitations (token_hash,email,expires_at) VALUES ($1,$2,$3)`, hash[:], email, time.Now().Add(time.Hour))
	require.NoError(t, err)
	return token
}

func TestInvitationIsEmailBoundAndConsumedExactlyOnce(t *testing.T) {
	pool := newTestPool(t)
	repo := repository.NewUserRepository(pool)
	ctx := context.Background()
	token := testInvitation(t, "invited@example.com")
	hash := sha256.Sum256([]byte(token))
	valid, err := repo.InvitationValid(ctx, hash[:], "other@example.com")
	require.NoError(t, err)
	require.False(t, valid)
	valid, err = repo.InvitationValid(ctx, hash[:], "INVITED@example.com")
	require.NoError(t, err)
	require.True(t, valid)
	errors := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			u := newTestUser()
			u.Email = "invited@example.com"
			u.ID = uuid.NewString()
			errors <- repo.CreateInvited(ctx, u, hash[:])
		})
	}
	wg.Wait()
	first, second := <-errors, <-errors
	if first != nil {
		first, second = second, first
	}
	require.NoError(t, first)
	require.ErrorIs(t, second, repository.ErrInvalidInvitation)
	valid, err = repo.InvitationValid(ctx, hash[:], "invited@example.com")
	require.NoError(t, err)
	require.False(t, valid)
}
