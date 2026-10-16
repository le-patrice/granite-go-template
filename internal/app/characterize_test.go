package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"example.com/app/internal/orders"
	"example.com/app/internal/platform/auth"
	"example.com/app/internal/platform/httpx"
	"example.com/app/internal/users"
)

// In-memory user repo for characterization tests.
type charUserRepo struct {
	users  map[uuid.UUID]*users.User
	tokens []*users.RefreshToken
}

func newCharUserRepo() *charUserRepo {
	return &charUserRepo{users: make(map[uuid.UUID]*users.User)}
}

func (r *charUserRepo) CreateUser(_ context.Context, email, passwordHash string, role users.Role) (users.User, error) {
	for _, u := range r.users {
		if strings.EqualFold(u.Email, email) {
			return users.User{}, users.ErrEmailTaken
		}
	}
	u := &users.User{
		ID:           uuid.New(),
		Email:        email,
		PasswordHash: passwordHash,
		Role:         role,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	r.users[u.ID] = u
	return *u, nil
}

func (r *charUserRepo) GetUserByEmail(_ context.Context, email string) (users.User, error) {
	for _, u := range r.users {
		if strings.EqualFold(u.Email, email) {
			return *u, nil
		}
	}
	return users.User{}, users.ErrNotFound
}

func (r *charUserRepo) GetUserByID(_ context.Context, id uuid.UUID) (users.User, error) {
	if u, ok := r.users[id]; ok {
		return *u, nil
	}
	return users.User{}, users.ErrNotFound
}

func (r *charUserRepo) UpdatePasswordHash(_ context.Context, id uuid.UUID, passwordHash string) error {
	if u, ok := r.users[id]; ok {
		u.PasswordHash = passwordHash
		return nil
	}
	return users.ErrNotFound
}

func (r *charUserRepo) CountAdmins(_ context.Context) (int64, error) {
	var n int64
	for _, u := range r.users {
		if u.Role == users.RoleAdmin {
			n++
		}
	}
	return n, nil
}

func (r *charUserRepo) InsertRefreshToken(_ context.Context, token users.RefreshToken) error {
	cp := token
	r.tokens = append(r.tokens, &cp)
	return nil
}

func (r *charUserRepo) RotateRefreshToken(_ context.Context, presentedHash, nextHash []byte, nextExpiresAt, now time.Time) (users.RefreshToken, error) {
	for _, t := range r.tokens {
		if !bytes.Equal(t.TokenHash, presentedHash) {
			continue
		}
		switch t.Verdict(now) {
		case users.VerdictReuse:
			_ = r.RevokeRefreshFamily(context.Background(), t.FamilyID)
			return users.RefreshToken{}, users.ErrTokenReuse
		case users.VerdictInvalid:
			return users.RefreshToken{}, users.ErrInvalidToken
		case users.VerdictOK:
		}
		rotated := now
		t.RotatedAt = &rotated
		newToken := &users.RefreshToken{
			ID:        uuid.New(),
			UserID:    t.UserID,
			FamilyID:  t.FamilyID,
			TokenHash: nextHash,
			ExpiresAt: nextExpiresAt,
			CreatedAt: now,
		}
		r.tokens = append(r.tokens, newToken)
		return *t, nil
	}
	return users.RefreshToken{}, users.ErrInvalidToken
}

func (r *charUserRepo) RevokeRefreshFamily(_ context.Context, familyID uuid.UUID) error {
	now := time.Now().UTC()
	for _, t := range r.tokens {
		if t.FamilyID == familyID && t.RevokedAt == nil {
			t.RevokedAt = &now
		}
	}
	return nil
}

func (r *charUserRepo) RevokeRefreshFamilyByHash(_ context.Context, tokenHash []byte) error {
	now := time.Now().UTC()
	for _, t := range r.tokens {
		if bytes.Equal(t.TokenHash, tokenHash) {
			for _, t2 := range r.tokens {
				if t2.FamilyID == t.FamilyID && t2.RevokedAt == nil {
					t2.RevokedAt = &now
				}
			}
			return nil
		}
	}
	return nil
}

// In-memory orders repo for characterization tests.
type charOrderRepo struct {
	orders []orders.Order
	nextID int64
}

func (r *charOrderRepo) CreateWithEvent(_ context.Context, in orders.NewOrder) (orders.Order, error) {
	for _, o := range r.orders {
		if o.OwnerID == in.OwnerID && o.Reference == in.Reference {
			return orders.Order{}, orders.ErrReferenceTaken
		}
	}
	r.nextID++
	o := orders.Order{
		ID:          r.nextID,
		OwnerID:     in.OwnerID,
		Reference:   in.Reference,
		AmountMinor: in.AmountMinor,
		Currency:    in.Currency,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	r.orders = append(r.orders, o)
	return o, nil
}

func (r *charOrderRepo) Get(_ context.Context, id int64) (orders.Order, error) {
	for _, o := range r.orders {
		if o.ID == id {
			return o, nil
		}
	}
	return orders.Order{}, orders.ErrNotFound
}

func (r *charOrderRepo) ListByOwner(_ context.Context, owner uuid.UUID, before int64, limit int) ([]orders.Order, error) {
	var out []orders.Order
	for i := len(r.orders) - 1; i >= 0 && len(out) < limit; i-- {
		o := r.orders[i]
		if o.OwnerID == owner && o.ID < before {
			out = append(out, o)
		}
	}
	return out, nil
}

func (r *charOrderRepo) ListAll(_ context.Context, before int64, limit int) ([]orders.Order, error) {
	var out []orders.Order
	for i := len(r.orders) - 1; i >= 0 && len(out) < limit; i-- {
		o := r.orders[i]
		if o.ID < before {
			out = append(out, o)
		}
	}
	return out, nil
}

type testHarness struct {
	router *httpx.Router
	tokens *auth.TokenManager
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	clock := func() time.Time { return time.Now().UTC() }
	hasher, err := auth.NewHasher(auth.PasswordParams{MemoryKiB: 64, Time: 1, Threads: 1})
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}

	tm, err := auth.NewTokenManager(auth.TokenConfig{
		Key:       bytes.Repeat([]byte{7}, 32),
		AccessTTL: 15 * time.Minute,
		Now:       clock,
	})
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}

	userRepo := newCharUserRepo()
	userService := users.NewService(userRepo, hasher, tm, users.Options{
		RefreshTTL: 24 * time.Hour,
		Now:        clock,
	})

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	userHandler := users.NewHandler(userService, logger)

	orderRepo := &charOrderRepo{}
	orderService := orders.NewService(orderRepo)
	orderHandler := orders.NewHandler(orderService, logger)

	router := httpx.NewRouter()
	api := httpx.NewAPI(router, httpx.APIConfig{
		Title:       "Test Service",
		Version:     "1.0.0",
		DocsEnabled: false,
	})
	noopMW := func(h http.Handler) http.Handler { return h }
	authMW := auth.RequireAuth(tm)

	userHandler.Routes(api, noopMW, authMW)
	orderHandler.Routes(api, authMW, nil)

	return &testHarness{router: router, tokens: tm}
}

func TestCharacterize_AuthAndUsers(t *testing.T) {
	h := newTestHarness(t)

	// 1. Register valid user
	regBody := `{"email": "alice@example.com", "password": "correct horse battery"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/auth/register", strings.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
	var userRes struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &userRes); err != nil {
		t.Fatalf("unmarshal user: %v", err)
	}
	if userRes.Email != "alice@example.com" {
		t.Errorf("email = %q, want alice@example.com", userRes.Email)
	}

	// 2. Duplicate registration -> 409 Conflict
	reqDup := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/auth/register", strings.NewReader(regBody))
	reqDup.Header.Set("Content-Type", "application/json")
	recDup := httptest.NewRecorder()
	h.router.ServeHTTP(recDup, reqDup)

	if recDup.Code != http.StatusConflict {
		t.Fatalf("duplicate register status = %d, want 409", recDup.Code)
	}

	// 3. Login
	loginBody := `{"email": "alice@example.com", "password": "correct horse battery"}`
	reqLogin := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/auth/login", strings.NewReader(loginBody))
	reqLogin.Header.Set("Content-Type", "application/json")
	recLogin := httptest.NewRecorder()
	h.router.ServeHTTP(recLogin, reqLogin)

	if recLogin.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body: %s", recLogin.Code, recLogin.Body.String())
	}
	var tokenRes struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(recLogin.Body.Bytes(), &tokenRes); err != nil {
		t.Fatalf("unmarshal tokens: %v", err)
	}
	if tokenRes.AccessToken == "" || tokenRes.RefreshToken == "" {
		t.Fatalf("expected non-empty tokens in response")
	}

	// 4. GET /v1/me with bearer token
	reqMe := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/me", nil)
	reqMe.Header.Set("Authorization", "Bearer "+tokenRes.AccessToken)
	recMe := httptest.NewRecorder()
	h.router.ServeHTTP(recMe, reqMe)

	if recMe.Code != http.StatusOK {
		t.Fatalf("me status = %d, want 200; body: %s", recMe.Code, recMe.Body.String())
	}

	// 5. GET /v1/me without token -> 401
	reqMeUnauth := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/me", nil)
	recMeUnauth := httptest.NewRecorder()
	h.router.ServeHTTP(recMeUnauth, reqMeUnauth)

	if recMeUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauth me status = %d, want 401", recMeUnauth.Code)
	}

	// 6. Refresh tokens
	refBody := fmt.Sprintf(`{"refresh_token": %q}`, tokenRes.RefreshToken)
	reqRef := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/auth/refresh", strings.NewReader(refBody))
	reqRef.Header.Set("Content-Type", "application/json")
	recRef := httptest.NewRecorder()
	h.router.ServeHTTP(recRef, reqRef)

	if recRef.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200; body: %s", recRef.Code, recRef.Body.String())
	}

	// 7. Logout -> 204
	reqOut := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/auth/logout", strings.NewReader(refBody))
	reqOut.Header.Set("Content-Type", "application/json")
	recOut := httptest.NewRecorder()
	h.router.ServeHTTP(recOut, reqOut)

	if recOut.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", recOut.Code)
	}
}

func TestCharacterize_Orders(t *testing.T) {
	h := newTestHarness(t)

	// First register & login user
	regBody := `{"email": "bob@example.com", "password": "correct horse battery"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/auth/register", strings.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)

	loginBody := `{"email": "bob@example.com", "password": "correct horse battery"}`
	reqLogin := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/auth/login", strings.NewReader(loginBody))
	reqLogin.Header.Set("Content-Type", "application/json")
	recLogin := httptest.NewRecorder()
	h.router.ServeHTTP(recLogin, reqLogin)

	var tokenRes struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(recLogin.Body.Bytes(), &tokenRes)

	// 1. Create order
	orderBody := `{"reference": "ORD-1234", "amount_minor": 4999, "currency": "USD"}`
	reqCreate := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/orders", strings.NewReader(orderBody))
	reqCreate.Header.Set("Content-Type", "application/json")
	reqCreate.Header.Set("Authorization", "Bearer "+tokenRes.AccessToken)
	recCreate := httptest.NewRecorder()
	h.router.ServeHTTP(recCreate, reqCreate)

	if recCreate.Code != http.StatusCreated {
		t.Fatalf("create order status = %d, want 201; body: %s", recCreate.Code, recCreate.Body.String())
	}
	var createdOrder struct {
		ID          int64  `json:"id"`
		Reference   string `json:"reference"`
		AmountMinor int64  `json:"amount_minor"`
		Currency    string `json:"currency"`
	}
	if err := json.Unmarshal(recCreate.Body.Bytes(), &createdOrder); err != nil {
		t.Fatalf("unmarshal order: %v", err)
	}
	if createdOrder.Reference != "ORD-1234" || createdOrder.AmountMinor != 4999 || createdOrder.Currency != "USD" {
		t.Errorf("created order mismatch: %+v", createdOrder)
	}

	// 2. Get order by ID
	reqGet := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/orders/1", nil)
	reqGet.Header.Set("Authorization", "Bearer "+tokenRes.AccessToken)
	recGet := httptest.NewRecorder()
	h.router.ServeHTTP(recGet, reqGet)

	if recGet.Code != http.StatusOK {
		t.Fatalf("get order status = %d, want 200", recGet.Code)
	}

	// 3. Get non-existent order -> 404
	reqNotFound := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/orders/999", nil)
	reqNotFound.Header.Set("Authorization", "Bearer "+tokenRes.AccessToken)
	recNotFound := httptest.NewRecorder()
	h.router.ServeHTTP(recNotFound, reqNotFound)

	if recNotFound.Code != http.StatusNotFound {
		t.Fatalf("not found order status = %d, want 404", recNotFound.Code)
	}

	// 4. List orders
	reqList := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/orders", nil)
	reqList.Header.Set("Authorization", "Bearer "+tokenRes.AccessToken)
	recList := httptest.NewRecorder()
	h.router.ServeHTTP(recList, reqList)

	if recList.Code != http.StatusOK {
		t.Fatalf("list orders status = %d, want 200", recList.Code)
	}
	var listRes struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(recList.Body.Bytes(), &listRes); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(listRes.Items) != 1 || listRes.Items[0].ID != 1 {
		t.Errorf("list mismatch: %+v", listRes)
	}
}
