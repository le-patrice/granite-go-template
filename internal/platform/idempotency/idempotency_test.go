package idempotency

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"
)

type memRow struct {
	hash   []byte
	status int
	body   []byte
}

type memStore struct {
	mu   sync.Mutex
	rows map[string]*memRow
}

func newMemStore() *memStore {
	return &memStore{rows: make(map[string]*memRow)}
}

func id(scope, key string) string { return scope + "\x00" + key }

func (s *memStore) Reserve(_ context.Context, scope, key string, hash []byte) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.rows[id(scope, key)]; exists {
		return false, nil
	}
	s.rows[id(scope, key)] = &memRow{hash: hash}
	return true, nil
}

func (s *memStore) Get(_ context.Context, scope, key string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id(scope, key)]
	if !ok {
		return Record{}, ErrNotFound
	}
	return Record{RequestHash: row.hash, Status: row.status, Body: row.body}, nil
}

func (s *memStore) Complete(_ context.Context, scope, key string, status int, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.rows[id(scope, key)]
	row.status, row.body = status, body
	return nil
}

func (s *memStore) Release(_ context.Context, scope, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, id(scope, key))
	return nil
}

func (s *memStore) DeleteExpired(context.Context, time.Time) (int64, error) { return 0, nil }

type handlerStub struct {
	calls    atomic.Int32
	status   int
	body     string
	location string
}

func (h *handlerStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.calls.Add(1)
	_, _ = io.Copy(io.Discard, r.Body)
	w.Header().Set("Content-Type", "application/json")
	if h.location != "" {
		w.Header().Set("Location", h.location)
	}
	w.WriteHeader(h.status)
	_, _ = io.WriteString(w, h.body)
}

func newGuard(t *testing.T, store Store, next http.Handler) http.Handler {
	t.Helper()
	mw, err := Middleware(Config{
		Store:        store,
		Scope:        func(r *http.Request) string { return r.Header.Get("X-Test-Scope") },
		MaxBodyBytes: 1024,
	})
	if err != nil {
		t.Fatalf("Middleware: %v", err)
	}
	return mw(next)
}

func send(h http.Handler, scope, key, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(body))
	if scope != "" {
		req.Header.Set("X-Test-Scope", scope)
	}
	if key != "" {
		req.Header.Set(Header, key)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestMiddlewareRequiresStoreAndScope(t *testing.T) {
	if _, err := Middleware(Config{Scope: func(*http.Request) string { return "u" }}); err == nil {
		t.Error("expected an error without a store")
	}
	if _, err := Middleware(Config{Store: newMemStore()}); err == nil {
		t.Error("expected an error without a scope")
	}
}

func TestRejectsMissingScopeAndBadKeys(t *testing.T) {
	next := &handlerStub{status: http.StatusCreated}
	h := newGuard(t, newMemStore(), next)

	tests := []struct {
		name       string
		scope      string
		key        string
		wantStatus int
	}{
		{"no scope", "", "k1", http.StatusUnauthorized},
		{"missing key", "alice", "", http.StatusBadRequest},
		{"key with a space", "alice", "bad key", http.StatusBadRequest},
		{"key too long", "alice", strings.Repeat("k", 256), http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rr := send(h, tt.scope, tt.key, "/v1/x", "{}"); rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
		})
	}
	if got := next.calls.Load(); got != 0 {
		t.Errorf("handler ran %d times for rejected requests", got)
	}
}

func TestFirstRequestRunsAndRepeatsReplayTheStoredResponse(t *testing.T) {
	next := &handlerStub{status: http.StatusCreated, body: `{"id":1}`, location: "/v1/x/1"}
	h := newGuard(t, newMemStore(), next)

	first := send(h, "alice", "key-1", "/v1/x", `{"a":1}`)
	if first.Code != http.StatusCreated || first.Header().Get(ReplayedHeader) != "" {
		t.Fatalf("first response = %d, replayed %q", first.Code, first.Header().Get(ReplayedHeader))
	}

	second := send(h, "alice", "key-1", "/v1/x", `{"a":1}`)
	if second.Code != http.StatusCreated || second.Body.String() != `{"id":1}` {
		t.Errorf("replay = %d %q, want the stored 201 body", second.Code, second.Body.String())
	}
	if second.Header().Get(ReplayedHeader) != "true" {
		t.Error("replay is missing the Idempotent-Replayed header")
	}
	if second.Header().Get("Location") != "/v1/x/1" || second.Header().Get("Content-Type") != "application/json" {
		t.Errorf("replay lost headers: %v", second.Header())
	}
	if got := next.calls.Load(); got != 1 {
		t.Errorf("handler ran %d times, want 1", got)
	}
}

func TestSameKeyWithADifferentRequestIs422(t *testing.T) {
	next := &handlerStub{status: http.StatusCreated}
	h := newGuard(t, newMemStore(), next)
	send(h, "alice", "key-1", "/v1/x", `{"a":1}`)

	tests := []struct {
		name string
		path string
		body string
	}{
		{"different body", "/v1/x", `{"a":2}`},
		{"different path", "/v1/y", `{"a":1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rr := send(h, "alice", "key-1", tt.path, tt.body); rr.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want 422", rr.Code)
			}
		})
	}
	if got := next.calls.Load(); got != 1 {
		t.Errorf("handler ran %d times, want 1", got)
	}
}

func TestScopesAreIsolated(t *testing.T) {
	next := &handlerStub{status: http.StatusCreated}
	h := newGuard(t, newMemStore(), next)

	send(h, "alice", "shared-key", "/v1/x", `{}`)
	rr := send(h, "bob", "shared-key", "/v1/x", `{}`)

	if rr.Header().Get(ReplayedHeader) != "" {
		t.Error("bob received alice's stored response")
	}
	if got := next.calls.Load(); got != 2 {
		t.Errorf("handler ran %d times, want 2 (one per scope)", got)
	}
}

func TestInFlightRequestReturns409ThenTheResponseIsReplayed(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	slow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		close(started)
		<-release
		w.WriteHeader(http.StatusCreated)
	})
	h := newGuard(t, newMemStore(), slow)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- send(h, "alice", "key-1", "/v1/x", `{}`) }()
	<-started

	inFlight := send(h, "alice", "key-1", "/v1/x", `{}`)
	if inFlight.Code != http.StatusConflict || inFlight.Header().Get("Retry-After") == "" {
		t.Errorf("concurrent duplicate = %d (Retry-After %q), want 409 with Retry-After",
			inFlight.Code, inFlight.Header().Get("Retry-After"))
	}

	close(release)
	if first := <-done; first.Code != http.StatusCreated {
		t.Fatalf("first request status = %d", first.Code)
	}
	replay := send(h, "alice", "key-1", "/v1/x", `{}`)
	if replay.Code != http.StatusCreated || replay.Header().Get(ReplayedHeader) != "true" {
		t.Errorf("after completion: %d, replayed %q", replay.Code, replay.Header().Get(ReplayedHeader))
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("handler ran %d times, want 1", got)
	}
}

func TestServerErrorsAreNotStoredSoRetriesRunAgain(t *testing.T) {
	next := &handlerStub{status: http.StatusInternalServerError}
	h := newGuard(t, newMemStore(), next)

	if rr := send(h, "alice", "key-1", "/v1/x", `{}`); rr.Code != http.StatusInternalServerError {
		t.Fatalf("first status = %d, want 500", rr.Code)
	}
	next.status = http.StatusCreated
	retry := send(h, "alice", "key-1", "/v1/x", `{}`)
	if retry.Code != http.StatusCreated || retry.Header().Get(ReplayedHeader) != "" {
		t.Errorf("retry = %d, replayed %q; want a fresh 201", retry.Code, retry.Header().Get(ReplayedHeader))
	}
	if replay := send(h, "alice", "key-1", "/v1/x", `{}`); replay.Header().Get(ReplayedHeader) != "true" {
		t.Error("the successful retry was not stored")
	}
	if got := next.calls.Load(); got != 2 {
		t.Errorf("handler ran %d times, want 2", got)
	}
}

func TestClientErrorsAreStoredAndReplayed(t *testing.T) {
	next := &handlerStub{status: http.StatusUnprocessableEntity, body: `{"detail":"bad"}`}
	h := newGuard(t, newMemStore(), next)

	send(h, "alice", "key-1", "/v1/x", `{}`)
	replay := send(h, "alice", "key-1", "/v1/x", `{}`)

	if replay.Code != http.StatusUnprocessableEntity || replay.Header().Get(ReplayedHeader) != "true" {
		t.Errorf("replay = %d, replayed %q", replay.Code, replay.Header().Get(ReplayedHeader))
	}
	if got := next.calls.Load(); got != 1 {
		t.Errorf("handler ran %d times, want 1", got)
	}
}

func TestPanicReleasesTheKey(t *testing.T) {
	var calls atomic.Int32
	flaky := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			panic("boom")
		}
		w.WriteHeader(http.StatusCreated)
	})
	h := newGuard(t, newMemStore(), flaky)

	func() {
		defer func() { _ = recover() }()
		send(h, "alice", "key-1", "/v1/x", `{}`)
	}()

	if rr := send(h, "alice", "key-1", "/v1/x", `{}`); rr.Code != http.StatusCreated {
		t.Errorf("retry after a panic = %d, want a fresh 201", rr.Code)
	}
}

func TestOversizedBodyIs413AndDoesNotReserveTheKey(t *testing.T) {
	next := &handlerStub{status: http.StatusCreated}
	h := newGuard(t, newMemStore(), next)

	if rr := send(h, "alice", "key-1", "/v1/x", strings.Repeat("x", 2048)); rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rr.Code)
	}
	if rr := send(h, "alice", "key-1", "/v1/x", `{}`); rr.Code != http.StatusCreated {
		t.Errorf("a valid request with the same key = %d, want 201", rr.Code)
	}
}

type sweepStore struct {
	Store
	cutoffs chan time.Time
}

func (s *sweepStore) DeleteExpired(_ context.Context, cutoff time.Time) (int64, error) {
	select {
	case s.cutoffs <- cutoff:
	default:
	}
	return 1, nil
}

func TestCleanupSweepsUntilCancelled(t *testing.T) {
	defer goleak.VerifyNone(t)

	store := &sweepStore{cutoffs: make(chan time.Time, 10)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Cleanup(ctx, store, 5*time.Millisecond, time.Hour, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	}()

	select {
	case cutoff := <-store.cutoffs:
		want := time.Now().Add(-time.Hour)
		if cutoff.Before(want.Add(-time.Minute)) || cutoff.After(want.Add(time.Second)) {
			t.Errorf("cutoff = %v, want about %v", cutoff, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Cleanup never swept")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Cleanup did not stop after cancellation")
	}
}

func TestCleanupWithoutAnIntervalReturnsImmediately(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		Cleanup(context.Background(), newMemStore(), 0, time.Hour, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Cleanup blocked with a zero interval")
	}
}
