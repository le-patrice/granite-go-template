package auth

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const testTTL = 15 * time.Minute

var testEpoch = time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

func testKey(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func newTestManager(t *testing.T, key []byte, now time.Time) *TokenManager {
	t.Helper()
	m, err := NewTokenManager(TokenConfig{
		Key:       key,
		AccessTTL: testTTL,
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	return m
}

func TestIssueAndParseAccess(t *testing.T) {
	m := newTestManager(t, testKey(1), testEpoch)
	userID := uuid.New()

	token, expiresAt, err := m.IssueAccess(userID, RoleAdmin)
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	if !strings.HasPrefix(token, "v4.local.") {
		t.Errorf("token = %q, want a v4.local token", token)
	}
	if !expiresAt.Equal(testEpoch.Add(testTTL)) {
		t.Errorf("expiresAt = %v, want %v", expiresAt, testEpoch.Add(testTTL))
	}

	principal, err := m.ParseAccess(token)
	if err != nil {
		t.Fatalf("ParseAccess: %v", err)
	}
	if principal.UserID != userID || principal.Role != RoleAdmin || principal.TokenID == "" {
		t.Errorf("principal = %+v", principal)
	}
}

func TestParseRejectsExpiredToken(t *testing.T) {
	issuer := newTestManager(t, testKey(1), testEpoch)
	token, _, err := issuer.IssueAccess(uuid.New(), RoleMember)
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	justBefore := newTestManager(t, testKey(1), testEpoch.Add(testTTL-time.Second))
	if _, err := justBefore.ParseAccess(token); err != nil {
		t.Errorf("token rejected one second before expiry: %v", err)
	}
	afterExpiry := newTestManager(t, testKey(1), testEpoch.Add(testTTL+time.Second))
	if _, err := afterExpiry.ParseAccess(token); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err = %v, want ErrInvalidToken after expiry", err)
	}
}

func TestParseRejectsTamperedToken(t *testing.T) {
	m := newTestManager(t, testKey(1), testEpoch)
	token, _, err := m.IssueAccess(uuid.New(), RoleMember)
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	flip := func(s string, i int) string {
		replacement := byte('A')
		if s[i] == 'A' {
			replacement = 'B'
		}
		return s[:i] + string(replacement) + s[i+1:]
	}
	for name, tampered := range map[string]string{
		"middle of payload": flip(token, len(token)/2),
		"last character":    flip(token, len(token)-1),
		"truncated":         token[:len(token)-4],
		"extended":          token + "AAAA",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := m.ParseAccess(tampered); !errors.Is(err, ErrInvalidToken) {
				t.Errorf("err = %v, want ErrInvalidToken", err)
			}
		})
	}
}

func TestParseRejectsWrongKey(t *testing.T) {
	token, _, err := newTestManager(t, testKey(1), testEpoch).IssueAccess(uuid.New(), RoleMember)
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	other := newTestManager(t, testKey(2), testEpoch)
	if _, err := other.ParseAccess(token); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err = %v, want ErrInvalidToken", err)
	}
}

func TestParseRejectsWrongVersionAndGarbage(t *testing.T) {
	m := newTestManager(t, testKey(1), testEpoch)
	for _, token := range []string{
		"",
		"not-a-token",
		"v3.local.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"v4.public.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"v2.local.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	} {
		if _, err := m.ParseAccess(token); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("ParseAccess(%q) err = %v, want ErrInvalidToken", token, err)
		}
	}
}

func TestTokenIDsAreUnique(t *testing.T) {
	m := newTestManager(t, testKey(1), testEpoch)
	userID := uuid.New()
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		token, _, err := m.IssueAccess(userID, RoleMember)
		if err != nil {
			t.Fatalf("IssueAccess: %v", err)
		}
		principal, err := m.ParseAccess(token)
		if err != nil {
			t.Fatalf("ParseAccess: %v", err)
		}
		if seen[principal.TokenID] {
			t.Fatalf("token ID %q repeated", principal.TokenID)
		}
		seen[principal.TokenID] = true
	}
}

func TestNewTokenManagerValidatesInput(t *testing.T) {
	if _, err := NewTokenManager(TokenConfig{Key: []byte("short"), AccessTTL: testTTL}); err == nil {
		t.Error("expected an error for a short key")
	}
	if _, err := NewTokenManager(TokenConfig{Key: testKey(1), AccessTTL: 0}); err == nil {
		t.Error("expected an error for a zero TTL")
	}
}

func TestRefreshTokenGenerationAndHash(t *testing.T) {
	m := newTestManager(t, testKey(1), testEpoch)

	token, hash, err := m.NewRefreshToken()
	if err != nil {
		t.Fatalf("NewRefreshToken: %v", err)
	}
	if len(token) != 43 {
		t.Errorf("len(token) = %d, want 43 (32 bytes, base64url, no padding)", len(token))
	}
	if len(hash) != 32 {
		t.Errorf("len(hash) = %d, want 32 (SHA-256)", len(hash))
	}
	if !bytes.Equal(hash, m.HashRefreshToken(token)) {
		t.Error("HashRefreshToken(token) does not match the returned hash")
	}
	if bytes.Contains(hash, []byte(token)) {
		t.Error("hash contains the raw token")
	}

	other, _, err := m.NewRefreshToken()
	if err != nil {
		t.Fatalf("NewRefreshToken: %v", err)
	}
	if other == token {
		t.Error("two refresh tokens are identical")
	}
}
