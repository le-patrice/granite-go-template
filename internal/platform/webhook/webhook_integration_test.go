//go:build integration

package webhook_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"example.com/app/internal/platform/httpx"
	"example.com/app/internal/platform/queue"
	"example.com/app/internal/platform/store/storetest"
	"example.com/app/internal/platform/webhook"
)

// testDB is set once by TestMain and shared by every test in this package.
var testDB *storetest.DB

func TestMain(m *testing.M) {
	os.Exit(storetest.Run(m, func(db *storetest.DB) { testDB = db },
		storetest.WithMigration(queue.MigrateUp),
	))
}

type headerVerifier struct{}

func (headerVerifier) Verify(_ []byte, header http.Header) error {
	if header.Get("X-Signature") != "secret" {
		return webhook.ErrInvalidSignature
	}
	return nil
}

func idFromBody(body []byte) (string, error) {
	var event struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		return "", err
	}
	return event.ID, nil
}

type failingInserter struct {
	err error
}

func (f failingInserter) InsertTx(context.Context, pgx.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, f.err
}

func newHandler(t *testing.T, pool *pgxpool.Pool, jobs webhook.JobInserter) http.Handler {
	t.Helper()
	ingest, err := webhook.Ingest(webhook.Config{
		DB:      pool,
		Jobs:    jobs,
		Sources: map[string]webhook.Source{"good": {Verifier: headerVerifier{}, ExternalID: idFromBody}},
		Logger:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	router := httpx.NewRouter()
	router.Handle("POST /v1/webhooks/{source}", ingest)
	return router
}

func insertClient(t *testing.T, pool *pgxpool.Pool) webhook.JobInserter {
	t.Helper()
	client, err := queue.NewInsertClient(pool)
	if err != nil {
		t.Fatalf("NewInsertClient: %v", err)
	}
	return client
}

func deliver(h http.Handler, signature, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/webhooks/good", strings.NewReader(body))
	req.Header.Set("X-Signature", signature)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func count(t *testing.T, pool *pgxpool.Pool, query string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

const (
	eventsQuery = `SELECT count(*) FROM inbound_events`
	jobsQuery   = `SELECT count(*) FROM river_job WHERE kind = 'webhook.event'`
)

func TestDuplicateDeliveryStoresAndEnqueuesOnce(t *testing.T) {
	pool := testDB.Pool(t)
	h := newHandler(t, pool, insertClient(t, pool))
	body := `{"id":"evt_1","type":"ping"}`

	for i := 1; i <= 3; i++ {
		if rr := deliver(h, "secret", body); rr.Code != http.StatusOK {
			t.Fatalf("delivery %d status = %d, want 200", i, rr.Code)
		}
	}
	if got := count(t, pool, eventsQuery); got != 1 {
		t.Errorf("stored events = %d, want 1", got)
	}
	if got := count(t, pool, jobsQuery); got != 1 {
		t.Errorf("enqueued jobs = %d, want 1", got)
	}

	if rr := deliver(h, "secret", `{"id":"evt_2","type":"ping"}`); rr.Code != http.StatusOK {
		t.Fatalf("a different event status = %d", rr.Code)
	}
	if got := count(t, pool, eventsQuery); got != 2 {
		t.Errorf("stored events = %d after a second event, want 2", got)
	}
}

func TestBadSignatureStoresNothing(t *testing.T) {
	pool := testDB.Pool(t)
	h := newHandler(t, pool, insertClient(t, pool))

	if rr := deliver(h, "wrong", `{"id":"evt_1"}`); rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
	if got := count(t, pool, eventsQuery); got != 0 {
		t.Errorf("stored events = %d, want 0", got)
	}
	if got := count(t, pool, jobsQuery); got != 0 {
		t.Errorf("enqueued jobs = %d, want 0", got)
	}
}

func TestEnqueueFailureReturns503AndRollsBackTheEvent(t *testing.T) {
	pool := testDB.Pool(t)
	h := newHandler(t, pool, failingInserter{err: errors.New("queue unavailable")})

	if rr := deliver(h, "secret", `{"id":"evt_1"}`); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	if got := count(t, pool, eventsQuery); got != 0 {
		t.Errorf("stored events = %d after a failed enqueue, want 0 (rolled back)", got)
	}

	// The sender retries; with a healthy queue the same event is accepted once.
	retry := newHandler(t, pool, insertClient(t, pool))
	if rr := deliver(retry, "secret", `{"id":"evt_1"}`); rr.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200", rr.Code)
	}
	if got := count(t, pool, eventsQuery); got != 1 {
		t.Errorf("stored events = %d after the retry, want 1", got)
	}
}

func TestWorkerDispatchesTheStoredEventToItsProcessor(t *testing.T) {
	pool := testDB.Pool(t)
	h := newHandler(t, pool, insertClient(t, pool))
	if rr := deliver(h, "secret", `{"id":"evt_9","type":"ping"}`); rr.Code != http.StatusOK {
		t.Fatalf("delivery status = %d", rr.Code)
	}

	seen := make(chan webhook.Event, 10)
	workers := queue.NewWorkers()
	webhook.RegisterWorker(workers, pool, map[string]webhook.Processor{
		"good": webhook.ProcessorFunc(func(_ context.Context, event webhook.Event) error {
			seen <- event
			return nil
		}),
	})
	client, err := queue.NewWorkerClient(pool, queue.WorkerConfig{
		Workers:     workers,
		Concurrency: 2,
		Logger:      slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
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
	case event := <-seen:
		if event.Source != "good" || event.ExternalID != "evt_9" {
			t.Errorf("event = %+v", event)
		}
		var payload struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.Type != "ping" {
			t.Errorf("payload = %s (%v), want the stored body", event.Payload, err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the worker did not dispatch the event in time")
	}
}
