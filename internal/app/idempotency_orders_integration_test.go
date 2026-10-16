//go:build integration

// template:example:file orders

package app

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"example.com/app/internal/orders"
	"example.com/app/internal/platform/auth"
	"example.com/app/internal/platform/httpx"
	"example.com/app/internal/platform/idempotency"
	"example.com/app/internal/platform/queue"
)

type ordersAPI struct {
	handler http.Handler
	tokens  *auth.TokenManager
	pool    *pgxpool.Pool
}

func newOrdersAPI(t *testing.T) ordersAPI {
	t.Helper()
	pool := testDB.Pool(t)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	tokens, err := auth.NewTokenManager(auth.TokenConfig{Key: bytes.Repeat([]byte{5}, 32), AccessTTL: time.Hour})
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	jobs, err := queue.NewInsertClient(pool)
	if err != nil {
		t.Fatalf("NewInsertClient: %v", err)
	}
	idempotent, err := idempotency.Middleware(idempotency.Config{
		Store: newIdempotencyStore(pool),
		Scope: principalScope,
	})
	if err != nil {
		t.Fatalf("idempotency.Middleware: %v", err)
	}

	router := httpx.NewRouter()
	orders.NewHandler(orders.NewService(orders.NewRepo(pool, jobs)), logger).Routes(router, auth.RequireAuth(tokens), idempotent)
	return ordersAPI{
		handler: httpx.Chain(router, httpx.Recover(logger), httpx.RequestID()),
		tokens:  tokens,
		pool:    pool,
	}
}

func (a ordersAPI) seedUser(t *testing.T, email string) uuid.UUID {
	t.Helper()
	var raw string
	err := a.pool.QueryRow(context.Background(),
		`INSERT INTO users (email, password_hash, role) VALUES ($1, 'x', 'member') RETURNING id::text`, email).Scan(&raw)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		t.Fatalf("parse id: %v", err)
	}
	return id
}

func (a ordersAPI) post(t *testing.T, user uuid.UUID, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	token, _, err := a.tokens.IssueAccess(user, "member")
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/orders", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if key != "" {
		req.Header.Set(idempotency.Header, key)
	}
	rr := httptest.NewRecorder()
	a.handler.ServeHTTP(rr, req)
	return rr
}

func (a ordersAPI) count(t *testing.T, query string) int {
	t.Helper()
	var n int
	if err := a.pool.QueryRow(context.Background(), query).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

const orderBody = `{"reference":"R-1","amount_minor":500,"currency":"USD"}`

func TestOrderCreationIsIdempotent(t *testing.T) {
	a := newOrdersAPI(t)
	alice := a.seedUser(t, "alice@example.com")
	bob := a.seedUser(t, "bob@example.com")

	first := a.post(t, alice, "key-1", orderBody)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d, body = %s", first.Code, first.Body.String())
	}
	second := a.post(t, alice, "key-1", orderBody)
	if second.Code != http.StatusCreated || second.Body.String() != first.Body.String() {
		t.Errorf("replay = %d %q, want the first response", second.Code, second.Body.String())
	}
	if second.Header().Get(idempotency.ReplayedHeader) != "true" || second.Header().Get("Location") != first.Header().Get("Location") {
		t.Errorf("replay headers = %v", second.Header())
	}
	if got := a.count(t, `SELECT count(*) FROM orders`); got != 1 {
		t.Errorf("orders = %d after a replay, want 1", got)
	}
	if got := a.count(t, `SELECT count(*) FROM river_job WHERE kind = 'order.created'`); got != 1 {
		t.Errorf("jobs = %d after a replay, want 1", got)
	}

	if rr := a.post(t, alice, "key-1", `{"reference":"R-2","amount_minor":500,"currency":"USD"}`); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("same key with a different body = %d, want 422", rr.Code)
	}
	if rr := a.post(t, alice, "", orderBody); rr.Code != http.StatusBadRequest {
		t.Errorf("missing key = %d, want 400", rr.Code)
	}
	if rr := a.post(t, bob, "key-1", orderBody); rr.Code != http.StatusCreated || rr.Header().Get(idempotency.ReplayedHeader) != "" {
		t.Errorf("another user with the same key = %d (replayed %q), want a fresh 201",
			rr.Code, rr.Header().Get(idempotency.ReplayedHeader))
	}
	if got := a.count(t, `SELECT count(*) FROM orders`); got != 2 {
		t.Errorf("orders = %d, want 2 (one per user)", got)
	}
}

func TestConcurrentDuplicatesCreateExactlyOneOrder(t *testing.T) {
	a := newOrdersAPI(t)
	alice := a.seedUser(t, "alice@example.com")

	const attempts = 10
	results := make([]*httptest.ResponseRecorder, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = a.post(t, alice, "contested", orderBody)
		}()
	}
	wg.Wait()

	created := 0
	for i, rr := range results {
		switch rr.Code {
		case http.StatusCreated:
			if rr.Header().Get(idempotency.ReplayedHeader) == "" {
				created++
			}
		case http.StatusConflict:
		default:
			t.Errorf("attempt %d status = %d, want 201 or 409", i, rr.Code)
		}
	}
	if created != 1 {
		t.Errorf("%d attempts ran the handler, want exactly 1", created)
	}
	if got := a.count(t, `SELECT count(*) FROM orders`); got != 1 {
		t.Errorf("orders = %d, want 1", got)
	}
}
