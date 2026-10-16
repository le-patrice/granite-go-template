package users

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"example.com/app/internal/platform/auth"
)

const goodPassword = "correct horse battery"

// fakeRepo is an in-memory Repository that applies the same rotation rules as
// the SQL implementation, so these tests exercise the service's behavior.
type fakeRepo struct {
	users  map[uuid.UUID]*User
	tokens []*RefreshToken
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{users: make(map[uuid.UUID]*User)}
}

func (f *fakeRepo) CreateUser(_ context.Context, email, passwordHash string, role Role) (User, error) {
	for _, u := range f.users {
		if strings.EqualFold(u.Email, email) {
			return User{}, ErrEmailTaken
		}
	}
	u := &User{ID: uuid.New(), Email: email, PasswordHash: passwordHash, Role: role}
	f.users[u.ID] = u
	return *u, nil
}

func (f *fakeRepo) GetUserByEmail(_ context.Context, email string) (User, error) {
	for _, u := range f.users {
		if strings.EqualFold(u.Email, email) {
			return *u, nil
		}
	}
	return User{}, ErrNotFound
}

func (f *fakeRepo) GetUserByID(_ context.Context, id uuid.UUID) (User, error) {
	if u, ok := f.users[id]; ok {
		return *u, nil
	}
	return User{}, ErrNotFound
}

func (f *fakeRepo) UpdatePasswordHash(_ context.Context, id uuid.UUID, passwordHash string) error {
	f.users[id].PasswordHash = passwordHash
	return nil
}

func (f *fakeRepo) CountAdmins(context.Context) (int64, error) {
	var n int64
	for _, u := range f.users {
		if u.Role == RoleAdmin {
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) InsertRefreshToken(_ context.Context, token RefreshToken) error {
	stored := token
	f.tokens = append(f.tokens, &stored)
	return nil
}

func (f *fakeRepo) RotateRefreshToken(_ context.Context, presentedHash, nextHash []byte, nextExpiresAt, now time.Time) (RefreshToken, error) {
	for _, t := range f.tokens {
		if !bytes.Equal(t.TokenHash, presentedHash) {
			continue
		}
		switch t.Verdict(now) {
		case VerdictReuse:
			f.revokeFamily(t.FamilyID, now)
			return RefreshToken{}, ErrTokenReuse
		case VerdictInvalid:
			return RefreshToken{}, ErrInvalidToken
		case VerdictOK:
		}
		rotated := now
		t.RotatedAt = &rotated
		f.tokens = append(f.tokens, &RefreshToken{
			UserID:    t.UserID,
			FamilyID:  t.FamilyID,
			TokenHash: nextHash,
			ExpiresAt: nextExpiresAt,
		})
		return *t, nil
	}
	return RefreshToken{}, ErrInvalidToken
}

func (f *fakeRepo) RevokeRefreshFamily(_ context.Context, familyID uuid.UUID) error {
	f.revokeFamily(familyID, time.Now())
	return nil
}

func (f *fakeRepo) RevokeRefreshFamilyByHash(_ context.Context, tokenHash []byte) error {
	for _, t := range f.tokens {
		if bytes.Equal(t.TokenHash, tokenHash) {
			f.revokeFamily(t.FamilyID, time.Now())
			return nil
		}
	}
	return nil
}

func (f *fakeRepo) revokeFamily(familyID uuid.UUID, at time.Time) {
	for _, t := range f.tokens {
		if t.FamilyID == familyID && t.RevokedAt == nil {
			revoked := at
			t.RevokedAt = &revoked
		}
	}
}

func (f *fakeRepo) families() map[uuid.UUID]int {
	out := make(map[uuid.UUID]int)
	for _, t := range f.tokens {
		out[t.FamilyID]++
	}
	return out
}

type countingHasher struct {
	*auth.Hasher
	dummyCalls int
}

func (c *countingHasher) VerifyDummy(password string) {
	c.dummyCalls++
	c.Hasher.VerifyDummy(password)
}

type testClock struct {
	now time.Time
}

func (c *testClock) Now() time.Time          { return c.now }
func (c *testClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

type harness struct {
	svc    *Service
	repo   *fakeRepo
	hasher *countingHasher
	tokens *auth.TokenManager
	clock  *testClock
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	clock := &testClock{now: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)}
	base, err := auth.NewHasher(auth.PasswordParams{MemoryKiB: 64, Time: 1, Threads: 1})
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	tokens, err := auth.NewTokenManager(auth.TokenConfig{
		Key:       bytes.Repeat([]byte{7}, 32),
		AccessTTL: 15 * time.Minute,
		Now:       clock.Now,
	})
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	repo := newFakeRepo()
	hasher := &countingHasher{Hasher: base}
	svc := NewService(repo, hasher, tokens, Options{RefreshTTL: 24 * time.Hour, Now: clock.Now})
	return &harness{svc: svc, repo: repo, hasher: hasher, tokens: tokens, clock: clock}
}

func (h *harness) registerAndLogin(t *testing.T, email string) (User, Session) {
	t.Helper()
	ctx := context.Background()
	user, err := h.svc.Register(ctx, email, goodPassword)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	session, err := h.svc.Login(ctx, email, goodPassword)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	return user, session
}

func TestRegister(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	user, err := h.svc.Register(ctx, "  Alice@Example.com ", goodPassword)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if user.Role != RoleMember {
		t.Errorf("Role = %q, want member", user.Role)
	}
	if user.Email != "Alice@Example.com" {
		t.Errorf("Email = %q, want the trimmed address", user.Email)
	}
	if user.PasswordHash == goodPassword || !strings.HasPrefix(user.PasswordHash, "$argon2id$") {
		t.Errorf("stored hash = %q, want an argon2id hash", user.PasswordHash)
	}

	if _, err := h.svc.Register(ctx, "alice@example.COM", goodPassword); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("duplicate email err = %v, want ErrEmailTaken", err)
	}
	if _, err := h.svc.Register(ctx, "not-an-email", goodPassword); !errors.Is(err, ErrInvalidEmail) {
		t.Errorf("bad email err = %v, want ErrInvalidEmail", err)
	}
	if _, err := h.svc.Register(ctx, "bob@example.com", "short"); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("weak password err = %v, want ErrWeakPassword", err)
	}
}

func TestLoginSuccessIssuesUsableTokens(t *testing.T) {
	h := newHarness(t)
	user, session := h.registerAndLogin(t, "alice@example.com")

	principal, err := h.tokens.ParseAccess(session.AccessToken)
	if err != nil {
		t.Fatalf("access token does not parse: %v", err)
	}
	if principal.UserID != user.ID || principal.Role != string(RoleMember) {
		t.Errorf("principal = %+v, want user %s as member", principal, user.ID)
	}
	if session.RefreshToken == "" || !session.RefreshExpiresAt.Equal(h.clock.Now().Add(24*time.Hour)) {
		t.Errorf("refresh token or expiry wrong: %+v", session)
	}
	if h.hasher.dummyCalls != 0 {
		t.Errorf("dummyCalls = %d, want 0 for a known user", h.hasher.dummyCalls)
	}
}

func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.Register(ctx, "alice@example.com", goodPassword); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := h.svc.Register(ctx, "disabled@example.com", goodPassword); err != nil {
		t.Fatalf("Register: %v", err)
	}
	disabledAt := h.clock.Now()
	for _, u := range h.repo.users {
		if u.Email == "disabled@example.com" {
			u.DisabledAt = &disabledAt
		}
	}

	tests := []struct {
		name          string
		email         string
		password      string
		wantDummyCall int
	}{
		{"wrong password", "alice@example.com", "wrong horse battery", 0},
		{"unknown user", "nobody@example.com", goodPassword, 1},
		{"disabled account with right password", "disabled@example.com", goodPassword, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := h.hasher.dummyCalls
			_, err := h.svc.Login(ctx, tt.email, tt.password)
			if err != ErrInvalidCredentials { //nolint:errorlint // the exact sentinel is the point of this test
				t.Errorf("err = %v, want exactly ErrInvalidCredentials", err)
			}
			if got := h.hasher.dummyCalls - before; got != tt.wantDummyCall {
				t.Errorf("dummy verifications = %d, want %d", got, tt.wantDummyCall)
			}
		})
	}
}

func TestLoginRehashesWhenParamsChange(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	user, err := h.svc.Register(ctx, "alice@example.com", goodPassword)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	stronger, err := auth.NewHasher(auth.PasswordParams{MemoryKiB: 128, Time: 2, Threads: 1})
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	svc := NewService(h.repo, stronger, h.tokens, Options{RefreshTTL: time.Hour, Now: h.clock.Now})
	if _, err := svc.Login(ctx, "alice@example.com", goodPassword); err != nil {
		t.Fatalf("Login: %v", err)
	}

	updated := h.repo.users[user.ID].PasswordHash
	if updated == user.PasswordHash || !strings.Contains(updated, "m=128,t=2,p=1") {
		t.Errorf("hash was not upgraded: %s", updated)
	}
}

func TestRefreshRotatesWithinOneFamily(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, first := h.registerAndLogin(t, "alice@example.com")

	second, err := h.svc.Refresh(ctx, first.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if second.RefreshToken == first.RefreshToken || second.AccessToken == "" {
		t.Errorf("refresh did not issue new tokens: %+v", second)
	}
	if families := h.repo.families(); len(families) != 1 {
		t.Errorf("families = %v, want rotation to stay in one family", families)
	}
	if _, err := h.svc.Refresh(ctx, second.RefreshToken); err != nil {
		t.Errorf("rotated token cannot be used again in turn: %v", err)
	}
}

func TestRefreshReuseRevokesTheWholeFamily(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, first := h.registerAndLogin(t, "alice@example.com")

	second, err := h.svc.Refresh(ctx, first.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if _, err := h.svc.Refresh(ctx, first.RefreshToken); !errors.Is(err, ErrTokenReuse) {
		t.Fatalf("reusing a rotated token err = %v, want ErrTokenReuse", err)
	}
	if _, err := h.svc.Refresh(ctx, second.RefreshToken); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("the newest token err = %v, want ErrInvalidToken after family revocation", err)
	}
}

func TestRefreshRejectsExpiredUnknownAndEmptyTokens(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, session := h.registerAndLogin(t, "alice@example.com")

	if _, err := h.svc.Refresh(ctx, ""); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("empty token err = %v, want ErrInvalidToken", err)
	}
	if _, err := h.svc.Refresh(ctx, "unknown-token"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("unknown token err = %v, want ErrInvalidToken", err)
	}
	h.clock.Advance(25 * time.Hour)
	if _, err := h.svc.Refresh(ctx, session.RefreshToken); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expired token err = %v, want ErrInvalidToken", err)
	}
}

func TestRefreshForDisabledUserRevokesTheFamily(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	user, session := h.registerAndLogin(t, "alice@example.com")

	disabledAt := h.clock.Now()
	h.repo.users[user.ID].DisabledAt = &disabledAt

	if _, err := h.svc.Refresh(ctx, session.RefreshToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken for a disabled user", err)
	}
	for _, token := range h.repo.tokens {
		if token.RevokedAt == nil {
			t.Error("a token of the disabled user's family is still live")
		}
	}
}

func TestLogoutRevokesTheFamilyAndIsIdempotent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, session := h.registerAndLogin(t, "alice@example.com")

	if err := h.svc.Logout(ctx, session.RefreshToken); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := h.svc.Refresh(ctx, session.RefreshToken); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("refresh after logout err = %v, want ErrInvalidToken", err)
	}
	if err := h.svc.Logout(ctx, "unknown-token"); err != nil {
		t.Errorf("logout with an unknown token err = %v, want nil", err)
	}
	if err := h.svc.Logout(ctx, ""); err != nil {
		t.Errorf("logout with an empty token err = %v, want nil", err)
	}
}

func TestMe(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	user, _ := h.registerAndLogin(t, "alice@example.com")

	got, err := h.svc.Me(ctx, user.ID)
	if err != nil || got.ID != user.ID {
		t.Fatalf("Me = %+v, %v", got, err)
	}
	if _, err := h.svc.Me(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id err = %v, want ErrNotFound", err)
	}
	disabledAt := h.clock.Now()
	h.repo.users[user.ID].DisabledAt = &disabledAt
	if _, err := h.svc.Me(ctx, user.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("disabled user err = %v, want ErrNotFound", err)
	}
}

func TestCreateAdminRefusesWhenAnAdminExists(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	admin, err := h.svc.CreateAdmin(ctx, "root@example.com", goodPassword)
	if err != nil {
		t.Fatalf("CreateAdmin: %v", err)
	}
	if admin.Role != RoleAdmin {
		t.Errorf("Role = %q, want admin", admin.Role)
	}
	if _, err := h.svc.CreateAdmin(ctx, "second@example.com", goodPassword); !errors.Is(err, ErrAdminExists) {
		t.Errorf("second admin err = %v, want ErrAdminExists", err)
	}
}
