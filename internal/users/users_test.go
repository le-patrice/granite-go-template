package users

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalizeEmail(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr error
	}{
		{"plain address", "alice@example.com", "alice@example.com", nil},
		{"surrounding spaces are trimmed", "  alice@example.com  ", "alice@example.com", nil},
		{"case is preserved", "Alice@Example.COM", "Alice@Example.COM", nil},
		{"empty", "", "", ErrInvalidEmail},
		{"spaces only", "   ", "", ErrInvalidEmail},
		{"no at sign", "alice.example.com", "", ErrInvalidEmail},
		{"display name", "Alice <alice@example.com>", "", ErrInvalidEmail},
		{"two addresses", "a@example.com, b@example.com", "", ErrInvalidEmail},
		{"too long", strings.Repeat("a", 250) + "@example.com", "", ErrInvalidEmail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeEmail(tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("email = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  error
	}{
		{"11 characters", strings.Repeat("a", 11), ErrWeakPassword},
		{"12 characters", strings.Repeat("a", 12), nil},
		{"12 multibyte characters", strings.Repeat("é", 12), nil},
		{"over the byte cap", strings.Repeat("a", MaxPasswordBytes+1), ErrWeakPassword},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidatePassword(tt.password); !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestRefreshTokenVerdict(t *testing.T) {
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	past := now.Add(-time.Minute)
	future := now.Add(time.Hour)

	tests := []struct {
		name  string
		token RefreshToken
		want  Verdict
	}{
		{"fresh token", RefreshToken{ExpiresAt: future}, VerdictOK},
		{"expired token", RefreshToken{ExpiresAt: past}, VerdictInvalid},
		{"expires exactly now", RefreshToken{ExpiresAt: now}, VerdictInvalid},
		{"rotated token is reuse", RefreshToken{ExpiresAt: future, RotatedAt: &past}, VerdictReuse},
		{"revoked token is invalid", RefreshToken{ExpiresAt: future, RevokedAt: &past}, VerdictInvalid},
		{"rotated then revoked is invalid", RefreshToken{ExpiresAt: future, RotatedAt: &past, RevokedAt: &past}, VerdictInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.token.Verdict(now); got != tt.want {
				t.Errorf("Verdict = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUserDisabled(t *testing.T) {
	now := time.Now()
	if (User{}).Disabled() {
		t.Error("a user without DisabledAt reports disabled")
	}
	if !(User{DisabledAt: &now}).Disabled() {
		t.Error("a user with DisabledAt does not report disabled")
	}
}
