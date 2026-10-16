//go:build integration

package users_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"example.com/app/internal/platform/auth"
	"example.com/app/internal/platform/httpx"
	"example.com/app/internal/platform/store/storetest"
	"example.com/app/internal/users"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testDB is set once by TestMain and shared by every test in this package.
var testDB *storetest.DB

func TestMain(m *testing.M) {
	os.Exit(storetest.Run(m, func(db *storetest.DB) { testDB = db }))
}

const password = "correct horse battery"

func newAPI(t *testing.T) (http.Handler, *pgxpool.Pool) {
	t.Helper()
	pool := testDB.Pool(t)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	hasher, err := auth.NewHasher(auth.PasswordParams{MemoryKiB: 64, Time: 1, Threads: 1})
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	tokens, err := auth.NewTokenManager(auth.TokenConfig{Key: bytes.Repeat([]byte{7}, 32), AccessTTL: 15 * time.Minute})
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	svc := users.NewService(users.NewRepo(pool), hasher, tokens, users.Options{RefreshTTL: time.Hour, Logger: logger})

	router := httpx.NewRouter()
	passthrough := func(next http.Handler) http.Handler { return next }
	users.NewHandler(svc, logger).Routes(router, passthrough, auth.RequireAuth(tokens))
	return httpx.Chain(router, httpx.Recover(logger), httpx.RequestID()), pool
}

func call(t *testing.T, h http.Handler, method, path, bearer string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

type session struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token"`
}

func readSession(t *testing.T, rr *httptest.ResponseRecorder) session {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var s session
	if err := json.Unmarshal(rr.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if s.AccessToken == "" || s.RefreshToken == "" || s.TokenType != "Bearer" {
		t.Fatalf("incomplete session: %+v", s)
	}
	return s
}

func registerAndLogin(t *testing.T, h http.Handler, email string) session {
	t.Helper()
	creds := map[string]string{"email": email, "password": password}
	if rr := call(t, h, http.MethodPost, "/v1/auth/register", "", creds); rr.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", rr.Code, rr.Body.String())
	}
	return readSession(t, call(t, h, http.MethodPost, "/v1/auth/login", "", creds))
}

func TestRegisterLoginAndMe(t *testing.T) {
	h, _ := newAPI(t)
	creds := map[string]string{"email": "Alice@Example.com", "password": password}

	rr := call(t, h, http.MethodPost, "/v1/auth/register", "", creds)
	if rr.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if bytes.Contains(rr.Body.Bytes(), []byte("password")) {
		t.Errorf("register response mentions the password: %s", rr.Body.String())
	}

	if rr := call(t, h, http.MethodPost, "/v1/auth/register", "", map[string]string{"email": "alice@example.com", "password": password}); rr.Code != http.StatusConflict {
		t.Errorf("duplicate register status = %d, want 409", rr.Code)
	}
	if rr := call(t, h, http.MethodPost, "/v1/auth/register", "", map[string]string{"email": "bob@example.com", "password": "short"}); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("weak password status = %d, want 422", rr.Code)
	}

	if rr := call(t, h, http.MethodPost, "/v1/auth/login", "", map[string]string{"email": "alice@example.com", "password": "wrong horse battery"}); rr.Code != http.StatusUnauthorized {
		t.Errorf("wrong password status = %d, want 401", rr.Code)
	}
	if rr := call(t, h, http.MethodPost, "/v1/auth/login", "", map[string]string{"email": "nobody@example.com", "password": password}); rr.Code != http.StatusUnauthorized {
		t.Errorf("unknown user status = %d, want 401", rr.Code)
	}

	s := readSession(t, call(t, h, http.MethodPost, "/v1/auth/login", "", creds))

	me := call(t, h, http.MethodGet, "/v1/me", s.AccessToken, nil)
	if me.Code != http.StatusOK {
		t.Fatalf("me status = %d, body = %s", me.Code, me.Body.String())
	}
	var got struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if got.Email != "Alice@Example.com" || got.Role != "member" {
		t.Errorf("me = %+v", got)
	}

	if rr := call(t, h, http.MethodGet, "/v1/me", "", nil); rr.Code != http.StatusUnauthorized {
		t.Errorf("me without token status = %d, want 401", rr.Code)
	}
	if rr := call(t, h, http.MethodGet, "/v1/me", "v4.local.garbage", nil); rr.Code != http.StatusUnauthorized {
		t.Errorf("me with a bad token status = %d, want 401", rr.Code)
	}
}

func TestRefreshRotationReuseAndRevocation(t *testing.T) {
	h, _ := newAPI(t)
	first := registerAndLogin(t, h, "alice@example.com")

	second := readSession(t, call(t, h, http.MethodPost, "/v1/auth/refresh", "", map[string]string{"refresh_token": first.RefreshToken}))
	if second.RefreshToken == first.RefreshToken {
		t.Fatal("refresh returned the same refresh token")
	}
	if rr := call(t, h, http.MethodGet, "/v1/me", second.AccessToken, nil); rr.Code != http.StatusOK {
		t.Errorf("new access token status = %d, want 200", rr.Code)
	}

	reuse := call(t, h, http.MethodPost, "/v1/auth/refresh", "", map[string]string{"refresh_token": first.RefreshToken})
	if reuse.Code != http.StatusUnauthorized {
		t.Fatalf("reuse of a rotated token status = %d, want 401", reuse.Code)
	}

	revoked := call(t, h, http.MethodPost, "/v1/auth/refresh", "", map[string]string{"refresh_token": second.RefreshToken})
	if revoked.Code != http.StatusUnauthorized {
		t.Errorf("newest token after reuse status = %d, want 401 (family revoked)", revoked.Code)
	}

	if rr := call(t, h, http.MethodPost, "/v1/auth/refresh", "", map[string]string{"refresh_token": "unknown"}); rr.Code != http.StatusUnauthorized {
		t.Errorf("unknown refresh token status = %d, want 401", rr.Code)
	}
}

func TestLogoutRevokesTheFamily(t *testing.T) {
	h, _ := newAPI(t)
	s := registerAndLogin(t, h, "alice@example.com")

	if rr := call(t, h, http.MethodPost, "/v1/auth/logout", "", map[string]string{"refresh_token": s.RefreshToken}); rr.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", rr.Code)
	}
	if rr := call(t, h, http.MethodPost, "/v1/auth/refresh", "", map[string]string{"refresh_token": s.RefreshToken}); rr.Code != http.StatusUnauthorized {
		t.Errorf("refresh after logout status = %d, want 401", rr.Code)
	}
	if rr := call(t, h, http.MethodPost, "/v1/auth/logout", "", map[string]string{"refresh_token": "unknown"}); rr.Code != http.StatusNoContent {
		t.Errorf("logout with an unknown token status = %d, want 204", rr.Code)
	}
}

func TestDisabledUserCannotLoginRefreshOrUseMe(t *testing.T) {
	h, pool := newAPI(t)
	s := registerAndLogin(t, h, "alice@example.com")

	if _, err := pool.Exec(t.Context(), `UPDATE users SET disabled_at = now()`); err != nil {
		t.Fatalf("disable user: %v", err)
	}

	creds := map[string]string{"email": "alice@example.com", "password": password}
	if rr := call(t, h, http.MethodPost, "/v1/auth/login", "", creds); rr.Code != http.StatusUnauthorized {
		t.Errorf("login status = %d, want 401 for a disabled account", rr.Code)
	}
	if rr := call(t, h, http.MethodPost, "/v1/auth/refresh", "", map[string]string{"refresh_token": s.RefreshToken}); rr.Code != http.StatusUnauthorized {
		t.Errorf("refresh status = %d, want 401 for a disabled account", rr.Code)
	}
	if rr := call(t, h, http.MethodGet, "/v1/me", s.AccessToken, nil); rr.Code != http.StatusUnauthorized {
		t.Errorf("me status = %d, want 401 for a disabled account", rr.Code)
	}
}

func TestRequestBodyRules(t *testing.T) {
	h, _ := newAPI(t)

	raw := func(contentType, body string) int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/auth/login", bytes.NewBufferString(body))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}
	if got := raw("text/plain", `{"email":"a@b.co","password":"x"}`); got != http.StatusUnsupportedMediaType {
		t.Errorf("wrong content type status = %d, want 415", got)
	}
	if got := raw("application/json", `{"email":"a@b.co","password":"x","admin":true}`); got != http.StatusBadRequest {
		t.Errorf("unknown field status = %d, want 400", got)
	}
}
