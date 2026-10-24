package service

import "context"

func (s *Service) acquirePasswordSlot(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case s.passwordSlots <- struct{}{}:
		return nil
	default:
		return ErrPasswordCapacity
	}
}

func (s *Service) hashPassword(ctx context.Context, password string) (string, error) {
	if err := s.acquirePasswordSlot(ctx); err != nil {
		return "", err
	}
	defer func() { <-s.passwordSlots }()
	return s.hasher.Hash(password)
}

func (s *Service) verifyPassword(ctx context.Context, password, hash string) (bool, error) {
	if err := s.acquirePasswordSlot(ctx); err != nil {
		return false, err
	}
	defer func() { <-s.passwordSlots }()
	return s.hasher.Verify(password, hash)
}
