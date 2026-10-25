package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/mail"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
)

// GetAccount returns the authenticated user's private account state.
func (s *Service) GetAccount(ctx context.Context, accessToken string) (*AccountInfo, error) {
	claims, err := s.parseToken(ctx, accessToken, "access")
	if err != nil {
		return nil, err
	}
	user, err := s.repo.GetByID(ctx, claims.UserID)
	if err != nil {
		return nil, ErrInvalidToken
	}
	return &AccountInfo{UserID: user.ID, Email: user.Email, Username: user.Username,
		EmailVerified: user.EmailVerifiedAt != nil, VerificationRequired: s.emailRequired(user),
		VerificationDeadline: s.legacyVerifyDeadline}, nil
}

// RequestEmailVerification requires the password even for legacy accounts: email
// ownership alone must not reclaim an account with an incorrectly recorded email.
func (s *Service) RequestEmailVerification(ctx context.Context, email, password string) error {
	if s.emails == nil {
		return ErrEmailUnavailable
	}
	release, err := s.emails.admit(ctx)
	if err != nil {
		return err
	}
	defer release()
	user, err := s.authenticate(ctx, email, password)
	if errors.Is(err, ErrInvalidCredentials) {
		return s.queueEmail(ctx, nil, "verify")
	}
	if err != nil {
		return err
	}
	if user.EmailVerifiedAt != nil {
		return s.queueEmail(ctx, nil, "verify")
	}
	return s.queueEmail(ctx, user, "verify")
}

// RequestPasswordReset deliberately gives unknown and unverified accounts the
// same result. A request never changes a password or revokes sessions.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	if s.emails == nil {
		return ErrEmailUnavailable
	}
	if err := validator.ValidateEmail(email); err != nil {
		return err
	}
	release, err := s.emails.admit(ctx)
	if err != nil {
		return err
	}
	defer release()
	user, err := s.repo.GetByEmail(ctx, email)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return ErrEmailUnavailable
	}
	if user == nil || user.EmailVerifiedAt == nil {
		user = nil
	}
	return s.queueEmail(ctx, user, "reset")
}

// RequestEmailChange requires a current session, the current password and a
// previously verified account email. Its result does not disclose target usage.
func (s *Service) RequestEmailChange(ctx context.Context, accessToken, password, newEmail string) error {
	if s.emails == nil {
		return ErrEmailUnavailable
	}
	if err := validator.ValidateEmail(newEmail); err != nil {
		return err
	}
	claims, err := s.parseToken(ctx, accessToken, "access")
	if err != nil {
		return err
	}
	user, err := s.repo.GetByID(ctx, claims.UserID)
	if err != nil {
		return ErrInvalidToken
	}
	if user.EmailVerifiedAt == nil {
		return ErrEmailUnverified
	}
	if strings.EqualFold(newEmail, user.Email) {
		return validator.ErrInvalidEmail
	}
	if err := s.allowPasswordAttempt(ctx, user.Email); err != nil {
		return err
	}
	match, err := s.verifyPassword(ctx, password, user.PasswordHash)
	if err != nil {
		return err
	}
	if !match {
		return ErrInvalidCredentials
	}
	action, payload, err := s.emails.challenge(user, "change", newEmail)
	if err != nil {
		return err
	}
	return emailRequestResult(s.emails.repo.QueueAction(ctx, action, payload))
}

func (s *Service) queueEmail(ctx context.Context, user *domain.User, purpose string) error {
	if user == nil {
		// Generate and encrypt the same bounded payload before the global capacity
		// probe, even though no actual notification may be sent for this address.
		dummy := &domain.User{ID: uuid.NewString(), Email: "unknown@example.invalid"}
		_, _, err := s.emails.challenge(dummy, purpose, dummy.Email)
		if err != nil {
			return err
		}
		return emailRequestResult(s.emails.repo.QueueAction(ctx, nil, nil))
	}
	action, payload, err := s.emails.challenge(user, purpose, user.Email)
	if err != nil {
		return err
	}
	return emailRequestResult(s.emails.repo.QueueAction(ctx, action, payload))
}

func emailRequestResult(err error) error {
	switch {
	case err == nil, errors.Is(err, repository.ErrEmailActionRateLimited), errors.Is(err, repository.ErrInvalidEmailAction):
		return nil
	default:
		return ErrEmailUnavailable
	}
}

// ConfirmEmailVerification consumes only an email-ownership proof.
func (s *Service) ConfirmEmailVerification(ctx context.Context, token string) (string, error) {
	return s.completeEmail(ctx, token, "verify", "")
}

// ConfirmPasswordReset changes credentials and invalidates every old session.
func (s *Service) ConfirmPasswordReset(ctx context.Context, token, password string) (string, error) {
	if err := validator.ValidatePassword(password); err != nil {
		return "", err
	}
	return s.completeEmail(ctx, token, "reset", password)
}

// ConfirmEmailChange commits ownership of a new email and ends existing sessions.
func (s *Service) ConfirmEmailChange(ctx context.Context, token string) (string, error) {
	return s.completeEmail(ctx, token, "change", "")
}

func (s *Service) completeEmail(ctx context.Context, token, purpose, password string) (string, error) {
	if s.emails == nil {
		return "", ErrEmailUnavailable
	}
	hash, err := emailTokenHash(token)
	if err != nil {
		return "", err
	}
	action, err := s.emails.repo.FindAction(ctx, hash, purpose)
	if errors.Is(err, repository.ErrInvalidEmailAction) || errors.Is(err, repository.ErrNotFound) {
		return "", ErrInvalidEmailAction
	}
	if err != nil {
		return "", ErrEmailUnavailable
	}
	// Expired/invalid proofs are rejected before any new Argon2 allocation.
	newHash := ""
	if purpose == "reset" {
		newHash, err = s.hashPassword(ctx, password)
		if err != nil {
			return "", err
		}
	}
	var notices [][]byte
	if purpose != "verify" {
		text := "Пароль аккаунта Vault Chat изменён. Все прежние сеансы завершены. Если это были не вы, восстановите доступ через сайт."
		if purpose == "change" {
			text = "Email аккаунта Vault Chat изменён. Все прежние сеансы завершены. Если это были не вы, обратитесь к оператору сервиса."
		}
		payload, err := s.emails.box.Seal(mail.Message{To: action.AccountEmail, Subject: "Изменения безопасности Vault Chat", Text: text})
		if err != nil {
			return "", ErrEmailUnavailable
		}
		notices = append(notices, payload)
		if purpose == "change" {
			payload, err = s.emails.box.Seal(mail.Message{To: action.TargetEmail, Subject: "Email Vault Chat подтверждён", Text: "Новый адрес аккаунта подтверждён. Войдите в Vault Chat заново."})
			if err != nil {
				return "", ErrEmailUnavailable
			}
			notices = append(notices, payload)
		}
	}
	id, err := s.emails.repo.CompleteAction(ctx, action, newHash, notices)
	if errors.Is(err, repository.ErrInvalidEmailAction) || errors.Is(err, repository.ErrNotFound) {
		return "", ErrInvalidEmailAction
	}
	if err != nil {
		return "", ErrEmailUnavailable
	}
	if purpose != "verify" {
		s.notifyRevocation(ctx, id)
	}
	s.logger.InfoContext(ctx, "email account action completed", "user_id", id, "purpose", purpose)
	return id, nil
}

func (a *EmailAccounts) challenge(user *domain.User, purpose, target string) (*domain.EmailAction, []byte, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, nil, ErrEmailUnavailable
	}
	token := hex.EncodeToString(random[:])
	hash := sha256.Sum256([]byte(token))
	ttl := 24 * time.Hour
	validFor := "24 часа"
	fragment := "verify-email"
	subject := "Подтвердите email Vault Chat"
	if purpose == "reset" {
		ttl = 15 * time.Minute
		validFor = "15 минут"
		fragment = "reset-password"
		subject = "Восстановление доступа Vault Chat"
	}
	if purpose == "change" {
		ttl = 30 * time.Minute
		validFor = "30 минут"
		fragment = "confirm-email-change"
		subject = "Подтвердите новый email Vault Chat"
	}
	action := &domain.EmailAction{ID: uuid.NewString(), UserID: user.ID, Purpose: purpose, AccountEmail: user.Email,
		TargetEmail: target, TokenHash: hash[:], CredentialVersion: user.CredentialVersion, ExpiresAt: time.Now().Add(ttl)}
	link := a.origin + "/#" + fragment + "=" + token
	text := fmt.Sprintf("%s\n\n%s\n\nСсылка действует %s. Открытие страницы ничего не меняет: подтвердите действие на сайте. Если вы не делали запрос, проигнорируйте письмо.", subject, link, validFor)
	payload, err := a.box.Seal(mail.Message{To: target, Subject: subject, Text: text})
	if err != nil {
		return nil, nil, ErrEmailUnavailable
	}
	return action, payload, nil
}

func emailTokenHash(token string) ([]byte, error) {
	if len(token) != 64 {
		return nil, ErrInvalidEmailAction
	}
	if decoded, err := hex.DecodeString(token); err != nil || len(decoded) != 32 {
		return nil, ErrInvalidEmailAction
	}
	hash := sha256.Sum256([]byte(token))
	return hash[:], nil
}

func (a *EmailAccounts) admit(ctx context.Context) (func(), error) {
	started := time.Now()
	select {
	case a.requests <- struct{}{}:
	default:
		return nil, ErrEmailUnavailable
	}
	return func() {
		defer func() { <-a.requests }()
		if remaining := 150*time.Millisecond - time.Since(started); remaining > 0 {
			timer := time.NewTimer(remaining)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
			}
		}
	}, nil
}
