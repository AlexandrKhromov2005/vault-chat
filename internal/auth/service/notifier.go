package service

import (
	"context"
	"time"
)

// SessionNotifier wakes connected gateways after a committed revocation.
type SessionNotifier interface {
	Notify(context.Context, string) error
}

// WithSessionNotifier adds best-effort low-latency revocation notifications.
// Database checks remain authoritative when notifications are lost.
func WithSessionNotifier(notifier SessionNotifier) Option {
	return func(s *Service) { s.notifier = notifier }
}

func (s *Service) notifyRevocation(ctx context.Context, userID string) {
	if s.notifier == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 250*time.Millisecond)
	defer cancel()
	if err := s.notifier.Notify(ctx, userID); err != nil {
		s.logger.WarnContext(ctx, "session revocation notification failed", "error", err)
	}
}
