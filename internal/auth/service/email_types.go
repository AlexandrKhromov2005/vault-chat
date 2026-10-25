package service

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/mail"
)

var (
	ErrInvalidEmailAction = errors.New("invalid or expired email action")
	ErrEmailUnavailable   = errors.New("email delivery unavailable")
)

// AccountInfo is private account state, never a public directory profile.
type AccountInfo struct {
	UserID, Email, Username             string
	EmailVerified, VerificationRequired bool
	VerificationDeadline                *time.Time
}

// EmailAccountRepository commits proofs, account changes and encrypted mail.
type EmailAccountRepository interface {
	CreateInvited(context.Context, *domain.User, []byte, *domain.EmailAction, []byte) error
	QueueAction(context.Context, *domain.EmailAction, []byte) error
	FindAction(context.Context, []byte, string) (*domain.EmailAction, error)
	CompleteAction(context.Context, *domain.EmailAction, string, [][]byte) (string, error)
}

// EmailAccounts configures the optional first-phase email workflows.
type EmailAccounts struct {
	repo     EmailAccountRepository
	box      *mail.Box
	origin   string
	requests chan struct{}
}

// NewEmailAccounts only accepts a configured HTTPS application origin. HTTP is
// permitted solely for explicit loopback development/test URLs.
func NewEmailAccounts(repo EmailAccountRepository, box *mail.Box, origin string) (*EmailAccounts, error) {
	u, err := url.Parse(origin)
	if err != nil || u == nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		u.Hostname() == "" || (u.Path != "" && u.Path != "/") || repo == nil || box == nil {
		return nil, ErrEmailUnavailable
	}
	loopback := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	allowedScheme := u.Scheme == "https" || (u.Scheme == "http" && loopback)
	if !allowedScheme {
		return nil, ErrEmailUnavailable
	}
	return &EmailAccounts{repo: repo, box: box, origin: strings.TrimSuffix(u.String(), "/"), requests: make(chan struct{}, 32)}, nil
}

// WithEmailAccounts enables pending registration and mail-based account actions.
func WithEmailAccounts(accounts *EmailAccounts) Option {
	return func(s *Service) { s.emails = accounts }
}
