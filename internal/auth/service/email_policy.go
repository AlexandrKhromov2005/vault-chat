package service

import (
	"errors"
	"time"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
)

// ErrEmailUnverified requires email ownership before a full session is issued.
var ErrEmailUnverified = errors.New("email verification required")

// WithLegacyVerificationDeadline sets an operator-controlled legacy transition.
func WithLegacyVerificationDeadline(deadline time.Time) Option {
	return func(s *Service) { s.legacyVerifyDeadline = &deadline }
}

func (s *Service) emailRequired(user *domain.User) bool {
	return user.EmailVerifiedAt == nil && (user.EmailVerificationRequired ||
		(s.legacyVerifyDeadline != nil && !time.Now().Before(*s.legacyVerifyDeadline)))
}
