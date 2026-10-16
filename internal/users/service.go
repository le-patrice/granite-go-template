package users

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Repository is the persistence the service needs. repo.go implements it.
type Repository interface {
	CreateUser(ctx context.Context, email, passwordHash string, role Role) (User, error)
	GetUserByEmail(ctx context.Context, email string) (User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (User, error)
	UpdatePasswordHash(ctx context.Context, id uuid.UUID, passwordHash string) error
	CountAdmins(ctx context.Context) (int64, error)

	InsertRefreshToken(ctx context.Context, token RefreshToken) error
	// RotateRefreshToken atomically marks the presented token rotated and
	// stores the next one in the same family. It returns the presented token's
	// record, ErrInvalidToken for unknown, expired, or revoked tokens, and
	// ErrTokenReuse (after revoking the family) for an already-rotated token.
	RotateRefreshToken(ctx context.Context, presentedHash, nextHash []byte, nextExpiresAt, now time.Time) (RefreshToken, error)
	RevokeRefreshFamily(ctx context.Context, familyID uuid.UUID) error
	RevokeRefreshFamilyByHash(ctx context.Context, tokenHash []byte) error
}

// Hasher hashes and verifies passwords.
type Hasher interface {
	Hash(password string) (string, error)
	Verify(encoded, password string) (bool, error)
	NeedsRehash(encoded string) bool
	VerifyDummy(password string)
}

// Tokens issues access tokens and generates refresh tokens.
type Tokens interface {
	IssueAccess(userID uuid.UUID, role string) (token string, expiresAt time.Time, err error)
	NewRefreshToken() (token string, hash []byte, err error)
	HashRefreshToken(token string) []byte
}

// Options are the service's tunables.
type Options struct {
	RefreshTTL time.Duration
	// Now is the clock. It defaults to time.Now in UTC and exists for tests.
	Now func() time.Time
	// Logger receives non-fatal warnings. It may be nil.
	Logger *slog.Logger
}

// Service holds the identity use-cases.
type Service struct {
	repo       Repository
	hasher     Hasher
	tokens     Tokens
	refreshTTL time.Duration
	now        func() time.Time
	logger     *slog.Logger
}

// NewService wires the use-cases to their dependencies.
func NewService(repo Repository, hasher Hasher, tokens Tokens, opts Options) *Service {
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{
		repo:       repo,
		hasher:     hasher,
		tokens:     tokens,
		refreshTTL: opts.RefreshTTL,
		now:        now,
		logger:     opts.Logger,
	}
}

// Register creates a member account.
func (s *Service) Register(ctx context.Context, email, password string) (User, error) {
	return s.create(ctx, email, password, RoleMember)
}

// CreateAdmin creates the first admin and refuses when any admin exists.
func (s *Service) CreateAdmin(ctx context.Context, email, password string) (User, error) {
	count, err := s.repo.CountAdmins(ctx)
	if err != nil {
		return User{}, fmt.Errorf("users: count admins: %w", err)
	}
	if count > 0 {
		return User{}, ErrAdminExists
	}
	return s.create(ctx, email, password, RoleAdmin)
}

func (s *Service) create(ctx context.Context, email, password string, role Role) (User, error) {
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return User{}, err
	}
	if err := ValidatePassword(password); err != nil {
		return User{}, err
	}
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return User{}, fmt.Errorf("users: hash password: %w", err)
	}
	return s.repo.CreateUser(ctx, normalized, hash, role)
}

// Login verifies credentials and starts a new refresh-token family. Unknown
// user, wrong password, and disabled account all return ErrInvalidCredentials,
// and the unknown-user path still spends a full password verification.
func (s *Service) Login(ctx context.Context, email, password string) (Session, error) {
	user, err := s.repo.GetUserByEmail(ctx, strings.TrimSpace(email))
	if errors.Is(err, ErrNotFound) {
		s.hasher.VerifyDummy(password)
		return Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, fmt.Errorf("users: find user: %w", err)
	}

	ok, err := s.hasher.Verify(user.PasswordHash, password)
	if err != nil {
		return Session{}, fmt.Errorf("users: verify password: %w", err)
	}
	if !ok || user.Disabled() {
		return Session{}, ErrInvalidCredentials
	}
	if s.hasher.NeedsRehash(user.PasswordHash) {
		s.rehash(ctx, user.ID, password)
	}

	refresh, hash, err := s.tokens.NewRefreshToken()
	if err != nil {
		return Session{}, fmt.Errorf("users: new refresh token: %w", err)
	}
	refreshExpires := s.now().Add(s.refreshTTL)
	err = s.repo.InsertRefreshToken(ctx, RefreshToken{
		UserID:    user.ID,
		FamilyID:  uuid.New(),
		TokenHash: hash,
		ExpiresAt: refreshExpires,
	})
	if err != nil {
		return Session{}, fmt.Errorf("users: store refresh token: %w", err)
	}
	return s.session(user, refresh, refreshExpires)
}

// Refresh rotates the presented refresh token. Presenting a token that was
// already rotated revokes its whole family.
func (s *Service) Refresh(ctx context.Context, presented string) (Session, error) {
	if presented == "" {
		return Session{}, ErrInvalidToken
	}
	next, nextHash, err := s.tokens.NewRefreshToken()
	if err != nil {
		return Session{}, fmt.Errorf("users: new refresh token: %w", err)
	}
	now := s.now()
	nextExpires := now.Add(s.refreshTTL)

	old, err := s.repo.RotateRefreshToken(ctx, s.tokens.HashRefreshToken(presented), nextHash, nextExpires, now)
	if err != nil {
		return Session{}, err
	}

	user, err := s.repo.GetUserByID(ctx, old.UserID)
	if errors.Is(err, ErrNotFound) || (err == nil && user.Disabled()) {
		if revokeErr := s.repo.RevokeRefreshFamily(ctx, old.FamilyID); revokeErr != nil {
			return Session{}, fmt.Errorf("users: revoke family: %w", revokeErr)
		}
		return Session{}, ErrInvalidToken
	}
	if err != nil {
		return Session{}, fmt.Errorf("users: find user: %w", err)
	}
	return s.session(user, next, nextExpires)
}

// Logout revokes the family of the presented refresh token. An unknown token
// is not an error, so logout reveals nothing and can be retried safely.
func (s *Service) Logout(ctx context.Context, presented string) error {
	if presented == "" {
		return nil
	}
	if err := s.repo.RevokeRefreshFamilyByHash(ctx, s.tokens.HashRefreshToken(presented)); err != nil {
		return fmt.Errorf("users: revoke family: %w", err)
	}
	return nil
}

// Me returns the account behind an authenticated request. A disabled or
// deleted account is reported as ErrNotFound.
func (s *Service) Me(ctx context.Context, id uuid.UUID) (User, error) {
	user, err := s.repo.GetUserByID(ctx, id)
	if err != nil {
		return User{}, err
	}
	if user.Disabled() {
		return User{}, ErrNotFound
	}
	return user, nil
}

func (s *Service) session(user User, refreshToken string, refreshExpires time.Time) (Session, error) {
	access, accessExpires, err := s.tokens.IssueAccess(user.ID, string(user.Role))
	if err != nil {
		return Session{}, fmt.Errorf("users: issue access token: %w", err)
	}
	return Session{
		AccessToken:      access,
		AccessExpiresAt:  accessExpires,
		RefreshToken:     refreshToken,
		RefreshExpiresAt: refreshExpires,
	}, nil
}

// rehash upgrades a stored hash after a successful login. Failure is logged
// and never blocks the login.
func (s *Service) rehash(ctx context.Context, id uuid.UUID, password string) {
	hash, err := s.hasher.Hash(password)
	if err == nil {
		err = s.repo.UpdatePasswordHash(ctx, id, hash)
	}
	if err != nil && s.logger != nil {
		s.logger.WarnContext(ctx, "password rehash failed", "user_id", id.String(), "error", err)
	}
}
