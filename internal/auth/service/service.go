package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/jwt"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/ratelimit"
)

var (
	// ErrPasswordCapacity rejects work before another Argon2 allocation.
	ErrPasswordCapacity = errors.New("service: password capacity reached")
	// ErrLoginRateLimited bounds attempts against an account across client IPs.
	ErrLoginRateLimited = errors.New("service: too many login attempts")
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
	SearchByUsernamePrefix(ctx context.Context, prefix, excludeID string, limit int) ([]*domain.User, error)
}

// SessionRepository is the session storage contract required by the service.
type SessionRepository interface {
	IsActive(ctx context.Context, userID, sessionID string) (bool, error)
	Create(ctx context.Context, session *domain.Session) error
	Rotate(ctx context.Context, currentID string, next *domain.Session, retryWindow time.Duration) (*domain.Session, error)
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
	IssueAccessToken(userID, email, username string, sessionID ...string) (string, time.Time, error)
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
	repo          UserRepository
	sessions      SessionRepository
	hasher        Hasher
	tokens        TokenManager
	logger        *slog.Logger
	dummyHash     string
	passwordSlots chan struct{}
	loginLimiter  *ratelimit.MemoryLimiter
	invitations   InvitationRepository
	notifier      SessionNotifier

	refreshRetryWindow time.Duration
}

// NewService wires a Service. A dummy hash is precomputed so that login
// attempts against unknown accounts cost the same Argon2id work as real ones.
func NewService(
	repo UserRepository,
	sessions SessionRepository,
	hasher Hasher,
	tokens TokenManager,
	logger *slog.Logger,
	options ...Option,
) (*Service, error) {
	dummyHash, err := hasher.Hash("timing-equalization-dummy")
	if err != nil {
		return nil, fmt.Errorf("failed to precompute dummy hash: %w", err)
	}

	s := &Service{
		repo:      repo,
		sessions:  sessions,
		hasher:    hasher,
		tokens:    tokens,
		logger:    logger,
		dummyHash: dummyHash,
		// Two default 64 MiB hashes leave room for the heap and runtime in
		// the 512 MiB Auth container. Admission never queues extra hash work.
		passwordSlots: make(chan struct{}, 2),
		loginLimiter:  ratelimit.NewMemoryLimiter(time.Minute, 5, 4096),

		refreshRetryWindow: DefaultRefreshRetryWindow,
	}
	for _, option := range options {
		option(s)
	}
	return s, nil
}

// Register validates the input, hashes the password, and stores the user.
func (s *Service) Register(ctx context.Context, email, username, password string) (*domain.User, error) {
	return s.register(ctx, email, username, password, s.repo.Create)
}

func (s *Service) register(ctx context.Context, email, username, password string, create func(context.Context, *domain.User) error) (*domain.User, error) {
	if err := validator.ValidateEmail(email); err != nil {
		return nil, fmt.Errorf("register: %w", err)
	}
	if err := validator.ValidateUsername(username); err != nil {
		return nil, fmt.Errorf("register: %w", err)
	}
	if err := validator.ValidatePassword(password); err != nil {
		return nil, fmt.Errorf("register: %w", err)
	}

	hash, err := s.hashPassword(ctx, password)
	if err != nil {
		return nil, fmt.Errorf("register: failed to hash password: %w", err)
	}

	user := &domain.User{
		ID:           uuid.NewString(),
		Email:        email,
		Username:     username,
		PasswordHash: hash,
	}

	if err := create(ctx, user); err != nil {
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
	// CITEXT email lookup is case-insensitive. Hash the same normalized key
	// for unknown accounts too; neither logs nor limiter keys contain email.
	account := sha256.Sum256([]byte(strings.ToLower(email)))
	quota, err := s.loginLimiter.Allow(ctx, hex.EncodeToString(account[:]))
	if err != nil {
		return nil, err
	}
	if !quota.Allowed {
		return nil, ErrLoginRateLimited
	}

	user, err := s.repo.GetByEmail(ctx, email)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return nil, fmt.Errorf("login: failed to load user: %w", err)
	}
	hash := s.dummyHash
	if user != nil {
		hash = user.PasswordHash
	}
	// Unknown accounts do the same bounded password work as real ones.
	match, err := s.verifyPassword(ctx, password, hash)
	if err != nil {
		return nil, fmt.Errorf("login: failed to verify password: %w", err)
	}
	if user == nil || !match {
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
	if want == jwt.AccessToken {
		if id, err := uuid.Parse(claims.SessionID); err != nil || id == uuid.Nil {
			return nil, ErrInvalidToken
		}
		active, err := s.sessions.IsActive(ctx, claims.UserID, claims.SessionID)
		if err != nil {
			return nil, fmt.Errorf("check access session: %w", err)
		}
		if !active {
			return nil, ErrInvalidToken
		}
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
	session := &domain.Session{ID: uuid.NewString(), UserID: user.ID}
	pair, err := s.issueTokens(user, session.ID)
	if err != nil {
		return nil, nil, err
	}
	session.ExpiresAt = pair.RefreshExpiresAt
	return pair, session, nil
}

// issueTokens mints an access/refresh token pair for user bound to the
// session sessionID.
func (s *Service) issueTokens(user *domain.User, sessionID string) (*TokenPair, error) {
	accessToken, accessExpiresAt, err := s.tokens.IssueAccessToken(user.ID, user.Email, user.Username, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to issue access token: %w", err)
	}

	refreshToken, refreshExpiresAt, err := s.tokens.IssueRefreshToken(sessionID, user.ID, user.Email, user.Username)
	if err != nil {
		return nil, fmt.Errorf("failed to issue refresh token: %w", err)
	}

	return &TokenPair{
		UserID:           user.ID,
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		AccessExpiresAt:  accessExpiresAt,
		RefreshExpiresAt: refreshExpiresAt,
	}, nil
}
