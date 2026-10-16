package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"aidanwoods.dev/go-paseto"
	"github.com/google/uuid"
)

const (
	refreshTokenBytes = 32
	tokenIDBytes      = 16
	roleClaim         = "role"
)

// ErrInvalidToken is returned for every kind of bad access token (expired,
// tampered, wrong key, wrong version, malformed) so callers cannot tell them apart.
var ErrInvalidToken = errors.New("auth: invalid token")

// TokenConfig configures a TokenManager.
type TokenConfig struct {
	// Key is the 32-byte symmetric key.
	Key       []byte
	AccessTTL time.Duration
	// Now is the clock. It defaults to time.Now and exists for tests.
	Now func() time.Time
}

// TokenManager issues and parses PASETO v4.local access tokens and generates
// opaque refresh tokens.
type TokenManager struct {
	key       paseto.V4SymmetricKey
	accessTTL time.Duration
	now       func() time.Time
}

// NewTokenManager validates cfg and builds a TokenManager.
func NewTokenManager(cfg TokenConfig) (*TokenManager, error) {
	key, err := paseto.V4SymmetricKeyFromBytes(cfg.Key)
	if err != nil {
		return nil, fmt.Errorf("auth: token key: %w", err)
	}
	if cfg.AccessTTL <= 0 {
		return nil, errors.New("auth: access token TTL must be positive")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &TokenManager{key: key, accessTTL: cfg.AccessTTL, now: now}, nil
}

// IssueAccess creates an access token carrying the subject, role, issued-at,
// expiry, and a random token ID. It also returns the expiry time.
func (m *TokenManager) IssueAccess(userID uuid.UUID, role string) (string, time.Time, error) {
	jti, err := randomHex(tokenIDBytes)
	if err != nil {
		return "", time.Time{}, err
	}
	issuedAt := m.now().UTC()
	expiresAt := issuedAt.Add(m.accessTTL)

	token := paseto.NewToken()
	token.SetSubject(userID.String())
	token.SetIssuedAt(issuedAt)
	token.SetNotBefore(issuedAt)
	token.SetExpiration(expiresAt)
	token.SetJti(jti)
	token.SetString(roleClaim, role)

	return token.V4Encrypt(m.key, nil), expiresAt, nil
}

// ParseAccess validates token and returns its principal. Every failure
// returns ErrInvalidToken.
func (m *TokenManager) ParseAccess(token string) (Principal, error) {
	parser := paseto.NewParserWithoutExpiryCheck()
	parser.AddRule(paseto.ValidAt(m.now()))

	parsed, err := parser.ParseV4Local(m.key, token, nil)
	if err != nil {
		return Principal{}, ErrInvalidToken
	}
	subject, err := parsed.GetSubject()
	if err != nil {
		return Principal{}, ErrInvalidToken
	}
	userID, err := uuid.Parse(subject)
	if err != nil {
		return Principal{}, ErrInvalidToken
	}
	role, err := parsed.GetString(roleClaim)
	if err != nil || role == "" {
		return Principal{}, ErrInvalidToken
	}
	jti, err := parsed.GetJti()
	if err != nil {
		return Principal{}, ErrInvalidToken
	}
	return Principal{UserID: userID, Role: role, TokenID: jti}, nil
}

// NewRefreshToken returns 32 random bytes as base64url and the SHA-256 hash to
// store. Only the hash is ever persisted.
func (m *TokenManager) NewRefreshToken() (token string, hash []byte, err error) {
	raw := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("auth: read refresh token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, m.HashRefreshToken(token), nil
}

// HashRefreshToken returns the SHA-256 hash of a presented refresh token.
func (m *TokenManager) HashRefreshToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: read random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}
