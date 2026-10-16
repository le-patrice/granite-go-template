// Package users is the identity feature: registration, login, refresh-token
// rotation, logout, and the current-user lookup.
package users

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	// MinPasswordLength is the minimum password length in characters.
	MinPasswordLength = 12
	// MaxPasswordBytes caps the password size accepted by the service.
	MaxPasswordBytes = 1024

	maxEmailLength = 254
)

// Domain errors. Their messages are safe to show to clients.
var (
	ErrInvalidEmail       = errors.New("email address is not valid")
	ErrWeakPassword       = fmt.Errorf("password must be between %d and %d characters long", MinPasswordLength, MaxPasswordBytes)
	ErrEmailTaken         = errors.New("email is already registered")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidToken       = errors.New("invalid refresh token")
	ErrTokenReuse         = errors.New("refresh token reuse detected")
	ErrNotFound           = errors.New("user not found")
	ErrAdminExists        = errors.New("an admin already exists")
)

// Role is a user's coarse permission level.
type Role string

// The roles stored in the users table.
const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

// User is an account.
type User struct {
	ID           uuid.UUID
	Email        string
	PasswordHash string
	Role         Role
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DisabledAt   *time.Time
}

// Disabled reports whether the account has been disabled.
func (u User) Disabled() bool {
	return u.DisabledAt != nil
}

// RefreshToken is the stored record of a refresh token. Only its hash exists.
type RefreshToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	FamilyID  uuid.UUID
	TokenHash []byte
	ExpiresAt time.Time
	RotatedAt *time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}

// Verdict is the outcome of presenting a refresh token.
type Verdict int

// The possible verdicts.
const (
	// VerdictOK means the token may be rotated.
	VerdictOK Verdict = iota
	// VerdictInvalid means the token is revoked or expired.
	VerdictInvalid
	// VerdictReuse means an already-rotated token was presented again, so the
	// whole family must be revoked.
	VerdictReuse
)

// Verdict applies the rotation rules to a stored token at time now.
func (t RefreshToken) Verdict(now time.Time) Verdict {
	switch {
	case t.RevokedAt != nil:
		return VerdictInvalid
	case t.RotatedAt != nil:
		return VerdictReuse
	case !now.Before(t.ExpiresAt):
		return VerdictInvalid
	default:
		return VerdictOK
	}
}

// Session is what a successful login or refresh hands back to the client.
type Session struct {
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
}

// NormalizeEmail trims raw and checks it is a single plain address.
func NormalizeEmail(raw string) (string, error) {
	email := strings.TrimSpace(raw)
	if email == "" || len(email) > maxEmailLength {
		return "", ErrInvalidEmail
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Name != "" || addr.Address != email {
		return "", ErrInvalidEmail
	}
	return email, nil
}

// ValidatePassword enforces the length rules.
func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < MinPasswordLength || len(password) > MaxPasswordBytes {
		return ErrWeakPassword
	}
	return nil
}
