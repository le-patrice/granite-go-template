//go:build integration

package acme_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"example.com/app/internal/integrations/acme"
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

const secret = "local-only-webhook-secret"

func newHandler(t *testing.T) (http.Handler, func(query string) int) {
	t.Helper()
	pool := testDB.Pool(t)
	jobs, err := queue.NewInsertClient(pool)
	if err != nil {
		t.Fatalf("NewInsertClient: %v", err)
	}
	source, err := acme.WebhookSource(secret)
	if err != nil {
		t.Fatalf("WebhookSource: %v", err)
	}
	ingest, err := webhook.Ingest(webhook.Config{
		DB:      pool,
		Jobs:    jobs,
		Sources: map[string]webhook.Source{"acme": source},
		Logger:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	router := httpx.NewRouter()
	router.Handle("POST /v1/webhooks/{source}", ingest)

	count := func(query string) int {
		var n int
		if err := pool.QueryRow(context.Background(), query).Scan(&n); err != nil {
			t.Fatalf("count %q: %v", query, err)
		}
		return n
	}
	return router, count
}

func post(h http.Handler, signature, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/webhooks/acme", strings.NewReader(body))
	if signature != "" {
		req.Header.Set(acme.SignatureHeader, signature)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestSignedDeliveriesAreAcceptedOnceAndForgeriesAreNot(t *testing.T) {
	h, count := newHandler(t)
	body := `{"id":"evt_1","type":"charge.succeeded","data":{"amount":500}}`

	if rr := post(h, acme.Sign(secret, []byte(body)), body); rr.Code != http.StatusOK {
		t.Fatalf("signed delivery status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if rr := post(h, acme.Sign(secret, []byte(body)), body); rr.Code != http.StatusOK {
		t.Fatalf("redelivery status = %d, want 200", rr.Code)
	}
	if got := count(`SELECT count(*) FROM inbound_events WHERE source = 'acme'`); got != 1 {
		t.Errorf("stored events = %d, want 1", got)
	}
	if got := count(`SELECT count(*) FROM river_job WHERE kind = 'webhook.event'`); got != 1 {
		t.Errorf("enqueued jobs = %d, want 1", got)
	}

	forged := `{"id":"evt_2","type":"charge.succeeded"}`
	if rr := post(h, acme.Sign("another-secret-value", []byte(forged)), forged); rr.Code != http.StatusForbidden {
		t.Errorf("forged delivery status = %d, want 403", rr.Code)
	}
	if rr := post(h, "", forged); rr.Code != http.StatusForbidden {
		t.Errorf("unsigned delivery status = %d, want 403", rr.Code)
	}
	if got := count(`SELECT count(*) FROM inbound_events`); got != 1 {
		t.Errorf("stored events = %d after forgeries, want still 1", got)
	}
}
