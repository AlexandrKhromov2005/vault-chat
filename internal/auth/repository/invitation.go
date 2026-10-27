package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
)

// ErrInvalidInvitation rejects expired, used and mismatched invitations alike.
var ErrInvalidInvitation = errors.New("invalid or expired invitation")

// ErrEmailPolicyRequired prevents delivery-off from reopening legacy signup.
var ErrEmailPolicyRequired = errors.New("email ownership policy requires mail-enabled registration")

// InvitationValid checks a high-entropy token before expensive password work.
func (r *UserRepository) InvitationValid(ctx context.Context, hash []byte, email string) (bool, error) {
	var valid bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM registration_invitations
WHERE token_hash=$1 AND email=$2 AND used_at IS NULL AND expires_at>now())`, hash, email).Scan(&valid)
	return valid, err
}

// CreateInvited consumes the invitation and creates the user atomically.
func (r *UserRepository) CreateInvited(ctx context.Context, user *domain.User, hash []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin invitation registration: %w", err)
	}
	defer rollbackEmailTransaction(ctx, tx)
	// Serialize the final legacy signup against the first mail-enrolled account.
	// The policy read runs after acquiring the lock, so it sees that commit.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('auth.email.outbox',0))`); err != nil {
		return err
	}
	var enrolled bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE email_verification_required)`).Scan(&enrolled); err != nil {
		return err
	}
	if enrolled {
		return ErrEmailPolicyRequired
	}
	var consumed []byte
	err = tx.QueryRow(ctx, `UPDATE registration_invitations SET used_at=clock_timestamp()
WHERE token_hash=$1 AND email=$2 AND used_at IS NULL AND expires_at>clock_timestamp() RETURNING token_hash`, hash, user.Email).Scan(&consumed)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidInvitation
	}
	if err != nil {
		return fmt.Errorf("consume invitation: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO users (id,email,username,password_hash) VALUES ($1::uuid,$2,$3,$4)`, user.ID, user.Email, user.Username, user.PasswordHash)
	if err != nil {
		return userCreationError(err)
	}
	return tx.Commit(ctx)
}
