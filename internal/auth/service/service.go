package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/jwt"
)

var (
	// ErrEmailTaken is returned when the email is already registered.
	ErrEmailTaken = errors.New("service: email already registered")
	// ErrUsernameTaken is returned when the username is already taken.
	ErrUsernameTaken = errors.New("service: username already taken")
	// ErrInvalidCredentials is returned on failed authentication. It is
	// deliberately generic to avoid leaking which part failed.
	ErrInvalidCredentials = errors.New("service: invalid credentials")
	// ErrInvalidToken is returned when a token fails validation.
	ErrInvalidToken = errors.New("service: invalid token")
)

// UserRepository is the storage contract required by the service. Defined at
// the consumer per CONTRIBUTING.md.
type UserRepository interface {
	Create(ctx context.Context, user *domain.User) error
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	GetByID(ctx context.Context, id string) (*domain.User, error)
}

// SessionRepository is the session storage contract required by the service.
type SessionRepository interface {
	Create(ctx context.Context, session *domain.Session) error
	Rotate(ctx context.Context, currentID string, next *domain.Session) error
	RevokeFamily(ctx context.Context, userID, sessionID string) error
	RevokeAllForUser(ctx context.Context, userID string) (int64, error)
}

// Hasher is the password hashing contract required by the service.
type Hasher interface {
	Hash(password string) (string, error)
	Verify(password, encodedHash string) (bool, error)
}

// TokenManager is the token issuance/validation contract required by the
// service.
type TokenManager interface {
	IssueAccessToken(userID, email, username string) (string, time.Time, error)
	IssueRefreshToken(sessionID, userID, email, username string) (string, time.Time, error)
	Validate(token string) (*jwt.Claims, error)
}

// TokenPair is a freshly issued access/refresh token bundle.
type TokenPair struct {
	UserID           string
	AccessToken      string
	RefreshToken     string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
}

// Service implements the auth business logic: registration, login, token
// validation, and session management.
type Service struct {
	repo      UserRepository
	sessions  SessionRepository
	hasher    Hasher
	tokens    TokenManager
	logger    *slog.Logger
	dummyHash string
}

// NewService wires a Service. A dummy hash is precomputed so that login
// attempts against unknown accounts cost the same Argon2id work as real ones.
func NewService(
	repo UserRepository,
	sessions SessionRepository,
	hasher Hasher,
	tokens TokenManager,
	logger *slog.Logger,
) (*Service, error) {
	dummyHash, err := hasher.Hash("timing-equalization-dummy")
	if err != nil {
		return nil, fmt.Errorf("failed to precompute dummy hash: %w", err)
	}

	return &Service{
		repo:      repo,
		sessions:  sessions,
		hasher:    hasher,
		tokens:    tokens,
		logger:    logger,
		dummyHash: dummyHash,
	}, nil
}

// Register validates the input, hashes the password, and stores the user.
func (s *Service) Register(ctx context.Context, email, username, password string) (*domain.User, error) {
	if err := validator.ValidateEmail(email); err != nil {
		return nil, fmt.Errorf("register: %w", err)
	}
	if err := validator.ValidateUsername(username); err != nil {
		return nil, fmt.Errorf("register: %w", err)
	}
	if err := validator.ValidatePassword(password); err != nil {
		return nil, fmt.Errorf("register: %w", err)
	}

	hash, err := s.hasher.Hash(password)
	if err != nil {
		return nil, fmt.Errorf("register: failed to hash password: %w", err)
	}

	user := &domain.User{
		ID:           uuid.NewString(),
		Email:        email,
		Username:     username,
		PasswordHash: hash,
	}

	if err := s.repo.Create(ctx, user); err != nil {
		switch {
		case errors.Is(err, repository.ErrEmailTaken):
			return nil, ErrEmailTaken
		case errors.Is(err, repository.ErrUsernameTaken):
			return nil, ErrUsernameTaken
		default:
			return nil, fmt.Errorf("register: failed to store user: %w", err)
		}
	}

	s.logger.InfoContext(ctx, "user registered", "user_id", user.ID)
	return user, nil
}

// Login authenticates a user by email and password and issues a token pair.
// Unknown accounts and wrong passwords produce the same error.
func (s *Service) Login(ctx context.Context, email, password string) (*TokenPair, error) {
	if err := validator.ValidateEmail(email); err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}
	if password == "" {
		return nil, ErrInvalidCredentials
	}

	user, err := s.repo.GetByEmail(ctx, email)
	if errors.Is(err, repository.ErrNotFound) {
		// Equalize timing with the real-password path so attackers cannot
		// probe account existence via response latency.
		_, _ = s.hasher.Verify(password, s.dummyHash)
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("login: failed to load user: %w", err)
	}

	match, err := s.hasher.Verify(password, user.PasswordHash)
	if err != nil {
		return nil, fmt.Errorf("login: failed to verify password: %w", err)
	}
	if !match {
		return nil, ErrInvalidCredentials
	}

	pair, session, err := s.issueTokenPair(user)
	if err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}
	if err := s.sessions.Create(ctx, session); err != nil {
		return nil, fmt.Errorf("login: failed to store session: %w", err)
	}

	s.logger.InfoContext(ctx, "user logged in", "user_id", user.ID)
	return pair, nil
}

// ValidateToken verifies an access token and returns its identity claims.
func (s *Service) ValidateToken(ctx context.Context, token string) (*jwt.Claims, error) {
	return s.parseToken(ctx, token, jwt.AccessToken)
}

// parseToken validates token and checks that it is of the wanted type. Every
// failure is reported as ErrInvalidToken.
func (s *Service) parseToken(ctx context.Context, token string, want jwt.TokenType) (*jwt.Claims, error) {
	if token == "" {
		return nil, ErrInvalidToken
	}

	claims, err := s.tokens.Validate(token)
	if err != nil {
		s.logger.DebugContext(ctx, "token validation failed", "error", err)
		return nil, ErrInvalidToken
	}
	if claims.TokenType != want {
		return nil, ErrInvalidToken
	}
	// The id of a refresh token is the id of its session.
	if want == jwt.RefreshToken {
		if _, err := uuid.Parse(claims.ID); err != nil {
			return nil, ErrInvalidToken
		}
	}
	return claims, nil
}

// issueTokenPair mints an access/refresh token pair for user together with
// the new session the refresh token is bound to. The session is not stored.
func (s *Service) issueTokenPair(user *domain.User) (*TokenPair, *domain.Session, error) {
	accessToken, accessExpiresAt, err := s.tokens.IssueAccessToken(user.ID, user.Email, user.Username)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to issue access token: %w", err)
	}

	session := &domain.Session{ID: uuid.NewString(), UserID: user.ID}
	refreshToken, refreshExpiresAt, err := s.tokens.IssueRefreshToken(session.ID, user.ID, user.Email, user.Username)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to issue refresh token: %w", err)
	}
	session.ExpiresAt = refreshExpiresAt

	return &TokenPair{
		UserID:           user.ID,
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		AccessExpiresAt:  accessExpiresAt,
		RefreshExpiresAt: refreshExpiresAt,
	}, session, nil
}
