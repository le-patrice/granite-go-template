//go:build integration

package orders_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"example.com/app/internal/orders"
	"example.com/app/internal/platform/auth"
	"example.com/app/internal/platform/httpx"
	"example.com/app/internal/platform/queue"
	"example.com/app/internal/platform/store/storetest"
)

// testDB is set once by TestMain and shared by every test in this package.
var testDB *storetest.DB

func TestMain(m *testing.M) {
	os.Exit(storetest.Run(m, func(db *storetest.DB) { testDB = db },
		storetest.WithMigration(queue.MigrateUp),
	))
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func seedUser(t *testing.T, pool *pgxpool.Pool, email, role string) uuid.UUID {
	t.Helper()
	var raw string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email, password_hash, role) VALUES ($1, 'x', $2) RETURNING id::text`,
		email, role).Scan(&raw)
	if err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		t.Fatalf("parse user id: %v", err)
	}
	return id
}

func count(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func insertClient(t *testing.T, pool *pgxpool.Pool) *river.Client[pgx.Tx] {
	t.Helper()
	client, err := queue.NewInsertClient(pool)
	if err != nil {
		t.Fatalf("NewInsertClient: %v", err)
	}
	return client
}

type failingInserter struct {
	err error
}

func (f failingInserter) InsertTx(context.Context, pgx.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, f.err
}

const jobsForKind = `SELECT count(*) FROM river_job WHERE kind = 'order.created'`

func TestOrderAndJobCommitTogether(t *testing.T) {
	pool := testDB.Pool(t)
	owner := seedUser(t, pool, "alice@example.com", "member")
	repo := orders.NewRepo(pool, insertClient(t, pool))

	order, err := repo.CreateWithEvent(context.Background(), orders.NewOrder{
		OwnerID: owner, Reference: "ref-1", AmountMinor: 1250, Currency: "USD",
	})
	if err != nil {
		t.Fatalf("CreateWithEvent: %v", err)
	}

	if got := count(t, pool, `SELECT count(*) FROM orders`); got != 1 {
		t.Errorf("orders = %d, want 1", got)
	}
	jobs := count(t, pool, `SELECT count(*) FROM river_job WHERE kind = 'order.created' AND (args->>'order_id')::bigint = $1`, order.ID)
	if jobs != 1 {
		t.Errorf("order.created jobs for the order = %d, want 1", jobs)
	}
}

func TestForcedFailureInsideTheTransactionLeavesNeither(t *testing.T) {
	pool := testDB.Pool(t)
	owner := seedUser(t, pool, "alice@example.com", "member")
	repo := orders.NewRepo(pool, failingInserter{err: errors.New("queue unavailable")})

	_, err := repo.CreateWithEvent(context.Background(), orders.NewOrder{
		OwnerID: owner, Reference: "ref-1", AmountMinor: 1250, Currency: "USD",
	})
	if err == nil {
		t.Fatal("CreateWithEvent succeeded although the job insert failed")
	}

	if got := count(t, pool, `SELECT count(*) FROM orders`); got != 0 {
		t.Errorf("orders = %d after a failed enqueue, want 0 (rolled back)", got)
	}
	if got := count(t, pool, jobsForKind); got != 0 {
		t.Errorf("jobs = %d after a failed enqueue, want 0", got)
	}
}

func TestDuplicateReferenceLeavesNoExtraJob(t *testing.T) {
	pool := testDB.Pool(t)
	owner := seedUser(t, pool, "alice@example.com", "member")
	repo := orders.NewRepo(pool, insertClient(t, pool))
	in := orders.NewOrder{OwnerID: owner, Reference: "same", AmountMinor: 1, Currency: "USD"}

	if _, err := repo.CreateWithEvent(context.Background(), in); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := repo.CreateWithEvent(context.Background(), in); !errors.Is(err, orders.ErrReferenceTaken) {
		t.Fatalf("second create err = %v, want ErrReferenceTaken", err)
	}
	if got := count(t, pool, jobsForKind); got != 1 {
		t.Errorf("jobs = %d, want 1", got)
	}
}

type chanNotifier struct {
	seen chan orders.Order
}

func (n *chanNotifier) OrderCreated(_ context.Context, order orders.Order) error {
	n.seen <- order
	return nil
}

func TestWorkerProcessesTheJob(t *testing.T) {
	pool := testDB.Pool(t)
	owner := seedUser(t, pool, "alice@example.com", "member")
	repo := orders.NewRepo(pool, insertClient(t, pool))

	order, err := repo.CreateWithEvent(context.Background(), orders.NewOrder{
		OwnerID: owner, Reference: "ref-1", AmountMinor: 1250, Currency: "USD",
	})
	if err != nil {
		t.Fatalf("CreateWithEvent: %v", err)
	}

	notifier := &chanNotifier{seen: make(chan orders.Order, 10)}
	workers := queue.NewWorkers()
	orders.RegisterWorkers(workers, orders.NewRepo(pool, nil), notifier)
	client, err := queue.NewWorkerClient(pool, queue.WorkerConfig{Workers: workers, Concurrency: 2, Logger: discardLogger()})
	if err != nil {
		t.Fatalf("NewWorkerClient: %v", err)
	}
	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := client.Stop(ctx); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})

	select {
	case got := <-notifier.seen:
		if got.ID != order.ID || got.Reference != "ref-1" {
			t.Errorf("notified order = %+v, want order %d", got, order.ID)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the worker did not process the order.created job in time")
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		var state string
		err := pool.QueryRow(context.Background(), `SELECT state FROM river_job WHERE kind = 'order.created'`).Scan(&state)
		if err == nil && state == "completed" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job state = %q (err %v), want completed", state, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type api struct {
	handler http.Handler
	tokens  *auth.TokenManager
}

func newAPI(t *testing.T) (api, *pgxpool.Pool) {
	t.Helper()
	pool := testDB.Pool(t)
	tokens, err := auth.NewTokenManager(auth.TokenConfig{Key: bytes.Repeat([]byte{9}, 32), AccessTTL: time.Hour})
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	svc := orders.NewService(orders.NewRepo(pool, insertClient(t, pool)))
	router := httpx.NewRouter()
	apiInstance := httpx.NewAPI(router, httpx.APIConfig{Title: "Test", Version: "1.0.0"})
	orders.NewHandler(svc, discardLogger()).Routes(apiInstance, auth.RequireAuth(tokens), nil)
	handler := httpx.Chain(router, httpx.Recover(discardLogger()), httpx.RequestID())
	return api{handler: handler, tokens: tokens}, pool
}

func (a api) call(t *testing.T, method, path string, user uuid.UUID, role string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if role != "" {
		token, _, err := a.tokens.IssueAccess(user, role)
		if err != nil {
			t.Fatalf("IssueAccess: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	a.handler.ServeHTTP(rr, req)
	return rr
}

type orderJSON struct {
	ID          int64  `json:"id"`
	OwnerID     string `json:"owner_id"`
	Reference   string `json:"reference"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

type listJSON struct {
	Items      []orderJSON `json:"items"`
	NextCursor string      `json:"next_cursor"`
}

func decode[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return v
}

func TestOwnerScopedAccessOverHTTP(t *testing.T) {
	a, pool := newAPI(t)
	alice := seedUser(t, pool, "alice@example.com", "member")
	bob := seedUser(t, pool, "bob@example.com", "member")
	admin := seedUser(t, pool, "root@example.com", "admin")

	create := a.call(t, http.MethodPost, "/v1/orders", alice, "member",
		map[string]any{"reference": "A-1", "amount_minor": 1250, "currency": "usd"})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", create.Code, create.Body.String())
	}
	created := decode[orderJSON](t, create)
	if created.Currency != "USD" || created.OwnerID != alice.String() {
		t.Errorf("created = %+v", created)
	}
	if got := create.Header().Get("Location"); got == "" {
		t.Error("create response has no Location header")
	}
	path := "/v1/orders/" + itoa(created.ID)

	if rr := a.call(t, http.MethodPost, "/v1/orders", alice, "member",
		map[string]any{"reference": "A-1", "amount_minor": 5, "currency": "USD"}); rr.Code != http.StatusConflict {
		t.Errorf("duplicate reference status = %d, want 409", rr.Code)
	}
	if rr := a.call(t, http.MethodPost, "/v1/orders", alice, "member",
		map[string]any{"reference": "A-2", "amount_minor": 0, "currency": "USD"}); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("zero amount status = %d, want 422", rr.Code)
	}

	for _, tt := range []struct {
		name   string
		user   uuid.UUID
		role   string
		status int
	}{
		{"owner", alice, "member", http.StatusOK},
		{"admin", admin, "admin", http.StatusOK},
		{"another member", bob, "member", http.StatusNotFound},
		{"unauthenticated", uuid.Nil, "", http.StatusUnauthorized},
	} {
		if rr := a.call(t, http.MethodGet, path, tt.user, tt.role, nil); rr.Code != tt.status {
			t.Errorf("GET as %s status = %d, want %d", tt.name, rr.Code, tt.status)
		}
	}

	for _, tt := range []struct {
		name string
		user uuid.UUID
		role string
		want int
	}{
		{"owner", alice, "member", 1},
		{"another member", bob, "member", 0},
		{"admin", admin, "admin", 1},
	} {
		rr := a.call(t, http.MethodGet, "/v1/orders", tt.user, tt.role, nil)
		if got := len(decode[listJSON](t, rr).Items); got != tt.want {
			t.Errorf("list as %s returned %d orders, want %d", tt.name, got, tt.want)
		}
	}
}

func TestKeysetPaginationOverHTTP(t *testing.T) {
	a, pool := newAPI(t)
	alice := seedUser(t, pool, "alice@example.com", "member")
	for _, ref := range []string{"A-1", "A-2", "A-3", "A-4", "A-5"} {
		rr := a.call(t, http.MethodPost, "/v1/orders", alice, "member",
			map[string]any{"reference": ref, "amount_minor": 1, "currency": "USD"})
		if rr.Code != http.StatusCreated {
			t.Fatalf("create %s status = %d", ref, rr.Code)
		}
	}

	path := "/v1/orders?limit=2"
	var ids []int64
	for pages := 0; pages < 5; pages++ {
		page := decode[listJSON](t, a.call(t, http.MethodGet, path, alice, "member", nil))
		for _, item := range page.Items {
			ids = append(ids, item.ID)
		}
		if page.NextCursor == "" {
			break
		}
		path = "/v1/orders?limit=2&cursor=" + page.NextCursor
	}
	if len(ids) != 5 {
		t.Fatalf("paged through %d orders, want 5", len(ids))
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] >= ids[i-1] {
			t.Errorf("ids are not strictly descending: %v", ids)
		}
	}
}

func TestBadInputOverHTTP(t *testing.T) {
	a, pool := newAPI(t)
	alice := seedUser(t, pool, "alice@example.com", "member")

	for _, tt := range []struct {
		name   string
		method string
		path   string
		status int
	}{
		{"non-numeric id", http.MethodGet, "/v1/orders/abc", http.StatusUnprocessableEntity},
		{"unknown id", http.MethodGet, "/v1/orders/999999", http.StatusNotFound},
		{"bad cursor", http.MethodGet, "/v1/orders?cursor=!!!", http.StatusBadRequest},
		{"bad limit", http.MethodGet, "/v1/orders?limit=abc", http.StatusUnprocessableEntity},
	} {
		if rr := a.call(t, tt.method, tt.path, alice, "member", nil); rr.Code != tt.status {
			t.Errorf("%s status = %d, want %d", tt.name, rr.Code, tt.status)
		}
	}
	if rr := a.call(t, http.MethodPost, "/v1/orders", uuid.Nil, "", map[string]any{"reference": "x"}); rr.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated create status = %d, want 401", rr.Code)
	}
}

func itoa(n int64) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}
