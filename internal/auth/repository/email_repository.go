package repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
)

var (
	// ErrInvalidEmailAction gives no distinction between unusable proofs.
	ErrInvalidEmailAction = errors.New("invalid or expired email action")
	// ErrEmailCapacity is global, including leased and scheduled work.
	ErrEmailCapacity = errors.New("email queue capacity reached")
	// ErrEmailActionRateLimited is masked for public email requests.
	ErrEmailActionRateLimited = errors.New("email action request quota reached")
)

const emailActionColumns = `id::text,user_id::text,purpose,account_email::text,target_email::text,token_hash,credential_version,expires_at`

// EmailRepository commits account proofs, credential changes and their encrypted
// notifications together. All producers lock outbox, then user, then action.
type EmailRepository struct{ pool *pgxpool.Pool }

// NewEmailRepository uses the auth database; payloads must already be encrypted.
func NewEmailRepository(pool *pgxpool.Pool) *EmailRepository { return &EmailRepository{pool: pool} }

func rollbackEmailTransaction(ctx context.Context, tx pgx.Tx) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(cleanup)
}

func (r *EmailRepository) transaction(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin email transaction: %w", err)
	}
	defer rollbackEmailTransaction(ctx, tx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('auth.email.outbox',0))`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM email_outbox WHERE expires_at<=clock_timestamp()`); err != nil {
		return err
	}
	if err = fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func lockEmailUser(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('auth.sessions:' || ($1::uuid)::text,0))`, id)
	return err
}

func emailCapacity(ctx context.Context, tx pgx.Tx, additional int) error {
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM email_outbox`).Scan(&count); err != nil {
		return err
	}
	if count+additional > 1000 {
		return ErrEmailCapacity
	}
	return nil
}

func validEmailPayload(payload []byte) bool { return len(payload) >= 29 && len(payload) <= 65565 }

func validEmailAction(a *domain.EmailAction) bool {
	if a == nil || len(a.TokenHash) != 32 || a.CredentialVersion < 0 {
		return false
	}
	for _, id := range []string{a.ID, a.UserID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil {
			return false
		}
	}
	if validator.ValidateEmail(a.AccountEmail) != nil || validator.ValidateEmail(a.TargetEmail) != nil {
		return false
	}
	return a.Purpose == "verify" || a.Purpose == "reset" || a.Purpose == "change"
}

func emailActionTTL(purpose string) time.Duration {
	switch purpose {
	case "verify":
		return 24 * time.Hour
	case "reset":
		return 15 * time.Minute
	default:
		return 30 * time.Minute
	}
}

func validateEmailExpiry(ctx context.Context, tx pgx.Tx, a *domain.EmailAction) error {
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return err
	}
	if !a.ExpiresAt.After(now) {
		return ErrInvalidEmailAction
	}
	// Cap application-supplied expiry against the DB clock without rejecting
	// requests solely because the application host's clock is slightly ahead.
	if maximum := now.Add(emailActionTTL(a.Purpose)); a.ExpiresAt.After(maximum) {
		a.ExpiresAt = maximum
	}
	return nil
}

func insertEmailAction(ctx context.Context, tx pgx.Tx, a *domain.EmailAction, consumed bool) error {
	_, err := tx.Exec(ctx, `INSERT INTO email_actions
(id,token_hash,user_id,purpose,account_email,target_email,credential_version,expires_at,consumed_at,created_at)
VALUES($1::uuid,$2,$3::uuid,$4,$5,$6,$7,$8,CASE WHEN $9 THEN clock_timestamp() END,clock_timestamp())`,
		a.ID, a.TokenHash, a.UserID, a.Purpose, a.AccountEmail, a.TargetEmail, a.CredentialVersion, a.ExpiresAt, consumed)
	return err
}

func insertEmailMessage(ctx context.Context, tx pgx.Tx, actionID *string, payload []byte, expires time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO email_outbox(id,action_id,payload,expires_at,available_at,created_at)
VALUES($1::uuid,$2::uuid,$3,$4,clock_timestamp(),clock_timestamp())`, uuid.NewString(), actionID, payload, expires)
	return err
}

// CreateInvited never inherits a caller's verified/legacy flags for a new public
// account. A failed queue insert also rolls back invitation consumption.
func (r *EmailRepository) CreateInvited(ctx context.Context, user *domain.User, inviteHash []byte, action *domain.EmailAction, payload []byte) error {
	if user == nil || !validEmailAction(action) || !validEmailPayload(payload) || len(inviteHash) != 32 ||
		action.Purpose != "verify" || action.UserID != user.ID || action.CredentialVersion != 0 ||
		!strings.EqualFold(action.AccountEmail, user.Email) || !strings.EqualFold(action.TargetEmail, user.Email) {
		return ErrInvalidEmailAction
	}
	local := *action
	action = &local
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := emailCapacity(ctx, tx, 1); err != nil {
			return err
		}
		if err := lockEmailUser(ctx, tx, user.ID); err != nil {
			return err
		}
		if err := validateEmailExpiry(ctx, tx, action); err != nil {
			return err
		}
		var consumed []byte
		err := tx.QueryRow(ctx, `UPDATE registration_invitations SET used_at=clock_timestamp()
WHERE token_hash=$1 AND email=$2 AND used_at IS NULL AND expires_at>clock_timestamp() RETURNING token_hash`, inviteHash, user.Email).Scan(&consumed)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidInvitation
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO users(id,email,username,password_hash,email_verification_required,email_verified_at,credential_version)
VALUES($1::uuid,$2,$3,$4,true,NULL,0)`, user.ID, user.Email, user.Username, user.PasswordHash)
		if err != nil {
			return userCreationError(err)
		}
		if err = insertEmailAction(ctx, tx, action, false); err != nil {
			return err
		}
		return insertEmailMessage(ctx, tx, &action.ID, payload, action.ExpiresAt)
	})
	if err == nil {
		user.EmailVerificationRequired = true
		user.EmailVerifiedAt = nil
		user.CredentialVersion = 0
	}
	return err
}

func emailAccount(ctx context.Context, tx pgx.Tx, id string) (*domain.User, error) {
	user := &domain.User{ID: id}
	err := tx.QueryRow(ctx, `SELECT email::text,credential_version,email_verified_at FROM users WHERE id=$1::uuid FOR UPDATE`, id).
		Scan(&user.Email, &user.CredentialVersion, &user.EmailVerifiedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidEmailAction
	}
	return user, err
}

func emailActionMatchesUser(a *domain.EmailAction, u *domain.User) bool {
	if !strings.EqualFold(a.AccountEmail, u.Email) || a.CredentialVersion != u.CredentialVersion {
		return false
	}
	if a.Purpose == "verify" {
		return u.EmailVerifiedAt == nil && strings.EqualFold(a.TargetEmail, u.Email)
	}
	return u.EmailVerifiedAt != nil && (a.Purpose == "change" || strings.EqualFold(a.TargetEmail, u.Email))
}

// QueueAction also implements the unknown-account capacity probe. Account quotas
// stay private at the service boundary; consumed metadata is retained for 24h.
func (r *EmailRepository) QueueAction(ctx context.Context, action *domain.EmailAction, payload []byte) error {
	if action != nil && (!validEmailAction(action) || !validEmailPayload(payload)) {
		return ErrInvalidEmailAction
	}
	if action != nil {
		local := *action
		action = &local
	}
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := emailCapacity(ctx, tx, 1); err != nil {
			return err
		}
		if action == nil {
			return nil
		}
		if err := lockEmailUser(ctx, tx, action.UserID); err != nil {
			return err
		}
		user, err := emailAccount(ctx, tx, action.UserID)
		if err != nil {
			return err
		}
		if !emailActionMatchesUser(action, user) {
			return ErrInvalidEmailAction
		}
		if err = validateEmailExpiry(ctx, tx, action); err != nil {
			return err
		}
		var minute, hour int
		err = tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE created_at>clock_timestamp()-interval '1 minute'),count(*)
FROM email_actions WHERE user_id=$1::uuid AND purpose=$2 AND created_at>clock_timestamp()-interval '1 hour'`, action.UserID, action.Purpose).Scan(&minute, &hour)
		if err != nil {
			return err
		}
		if minute >= 1 || hour >= 5 {
			return ErrEmailActionRateLimited
		}
		if action.Purpose == "change" {
			var owned bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE email=$1 AND id<>$2::uuid)`, action.TargetEmail, action.UserID).Scan(&owned); err != nil {
				return err
			}
			if owned {
				return insertEmailAction(ctx, tx, action, true)
			}
		}
		if action.Purpose != "reset" {
			if err = invalidateEmailActions(ctx, tx, action.UserID, action.Purpose, ""); err != nil {
				return err
			}
		}
		if err = insertEmailAction(ctx, tx, action, false); err != nil {
			return err
		}
		return insertEmailMessage(ctx, tx, &action.ID, payload, action.ExpiresAt)
	})
}

func scanEmailAction(row pgx.Row) (*domain.EmailAction, error) {
	a := &domain.EmailAction{}
	err := row.Scan(&a.ID, &a.UserID, &a.Purpose, &a.AccountEmail, &a.TargetEmail, &a.TokenHash, &a.CredentialVersion, &a.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidEmailAction
	}
	return a, err
}

// FindAction rejects unusable proofs before callers spend password hashing work.
func (r *EmailRepository) FindAction(ctx context.Context, hash []byte, purpose string) (*domain.EmailAction, error) {
	if len(hash) != 32 || (purpose != "verify" && purpose != "reset" && purpose != "change") {
		return nil, ErrInvalidEmailAction
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	columns := "a." + strings.ReplaceAll(emailActionColumns, ",", ",a.")
	return scanEmailAction(r.pool.QueryRow(ctx, `SELECT `+columns+` FROM email_actions a JOIN users u ON u.id=a.user_id
WHERE a.token_hash=$1 AND a.purpose=$2 AND a.consumed_at IS NULL AND a.expires_at>clock_timestamp()
AND a.account_email=u.email AND a.credential_version=u.credential_version
AND ((a.purpose='verify' AND u.email_verified_at IS NULL) OR (a.purpose<>'verify' AND u.email_verified_at IS NOT NULL))`, hash, purpose))
}

func invalidateEmailActions(ctx context.Context, tx pgx.Tx, userID, purpose, except string) error {
	_, err := tx.Exec(ctx, `DELETE FROM email_outbox WHERE action_id IN
(SELECT id FROM email_actions WHERE user_id=$1::uuid AND ($2='' OR purpose=$2))`, userID, purpose)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE email_actions SET consumed_at=clock_timestamp()
WHERE user_id=$1::uuid AND ($2='' OR purpose=$2) AND id::text<>$3 AND consumed_at IS NULL`, userID, purpose, except)
	return err
}

func sameEmailAction(a, b *domain.EmailAction) bool {
	return uuid.MustParse(a.ID) == uuid.MustParse(b.ID) && uuid.MustParse(a.UserID) == uuid.MustParse(b.UserID) &&
		a.Purpose == b.Purpose && a.CredentialVersion == b.CredentialVersion && bytes.Equal(a.TokenHash, b.TokenHash) &&
		strings.EqualFold(a.AccountEmail, b.AccountEmail) && strings.EqualFold(a.TargetEmail, b.TargetEmail)
}

// CompleteAction fences stale password/session work with a credential version and
// consumes the proof last, after every potentially blocking effect, using DB time.
func (r *EmailRepository) CompleteAction(ctx context.Context, action *domain.EmailAction, passwordHash string, notices [][]byte) (string, error) {
	if !validEmailAction(action) {
		return "", ErrInvalidEmailAction
	}
	wantNotices := 0
	switch action.Purpose {
	case "reset":
		wantNotices = 1
	case "change":
		wantNotices = 2
	}
	if len(notices) != wantNotices || (action.Purpose == "reset" && (passwordHash == "" || len(passwordHash) > 4096)) ||
		(action.Purpose != "reset" && passwordHash != "") {
		return "", ErrInvalidEmailAction
	}
	for _, notice := range notices {
		if !validEmailPayload(notice) {
			return "", ErrInvalidEmailAction
		}
	}
	var userID string
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := lockEmailUser(ctx, tx, action.UserID); err != nil {
			return err
		}
		user, err := emailAccount(ctx, tx, action.UserID)
		if err != nil {
			return err
		}
		stored, err := scanEmailAction(tx.QueryRow(ctx, `SELECT `+emailActionColumns+` FROM email_actions
WHERE token_hash=$1 AND purpose=$2 AND consumed_at IS NULL AND expires_at>clock_timestamp() FOR UPDATE`, action.TokenHash, action.Purpose))
		if err != nil {
			return err
		}
		if !sameEmailAction(stored, action) || !emailActionMatchesUser(stored, user) {
			return ErrInvalidEmailAction
		}
		// Revoking challenges frees their slots in this same globally locked tx.
		var remaining int
		err = tx.QueryRow(ctx, `SELECT count(*) FROM email_outbox o WHERE action_id IS NULL OR NOT EXISTS
(SELECT 1 FROM email_actions a WHERE a.id=o.action_id AND a.user_id=$1::uuid AND ($2<>'verify' OR a.purpose='verify'))`, user.ID, stored.Purpose).Scan(&remaining)
		if err != nil {
			return err
		}
		if remaining+len(notices) > 1000 {
			return ErrEmailCapacity
		}
		switch stored.Purpose {
		case "verify":
			_, err = tx.Exec(ctx, `UPDATE users SET email_verified_at=clock_timestamp(),email_verification_required=true,updated_at=clock_timestamp() WHERE id=$1::uuid`, user.ID)
		case "reset":
			_, err = tx.Exec(ctx, `UPDATE users SET password_hash=$2,credential_version=credential_version+1,updated_at=clock_timestamp() WHERE id=$1::uuid`, user.ID, passwordHash)
		case "change":
			_, err = tx.Exec(ctx, `UPDATE users SET email=$2,email_verified_at=clock_timestamp(),credential_version=credential_version+1,updated_at=clock_timestamp() WHERE id=$1::uuid`, user.ID, stored.TargetEmail)
			if err != nil && errors.Is(userCreationError(err), ErrEmailTaken) {
				return ErrInvalidEmailAction
			}
		}
		if err != nil {
			return err
		}
		purpose := ""
		if stored.Purpose == "verify" {
			purpose = "verify"
		} else {
			if _, err = tx.Exec(ctx, `UPDATE sessions SET revoked_at=clock_timestamp() WHERE user_id=$1::uuid AND revoked_at IS NULL`, user.ID); err != nil {
				return err
			}
		}
		if err = invalidateEmailActions(ctx, tx, user.ID, purpose, stored.ID); err != nil {
			return err
		}
		var noticeExpiry time.Time
		if err = tx.QueryRow(ctx, `SELECT clock_timestamp()+interval '24 hours'`).Scan(&noticeExpiry); err != nil {
			return err
		}
		for _, notice := range notices {
			if err = insertEmailMessage(ctx, tx, nil, notice, noticeExpiry); err != nil {
				return err
			}
		}
		err = tx.QueryRow(ctx, `UPDATE email_actions SET consumed_at=clock_timestamp()
WHERE id=$1::uuid AND consumed_at IS NULL AND expires_at>clock_timestamp() RETURNING user_id::text`, stored.ID).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidEmailAction
		}
		return err
	})
	if err != nil {
		return "", err
	}
	return userID, nil
}
