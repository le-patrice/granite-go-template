package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"example.com/app/internal/platform/httpx"
)

// headerVerifier accepts a delivery whose X-Signature header equals want.
type headerVerifier struct {
	want string
}

func (v headerVerifier) Verify(_ []byte, header http.Header) error {
	if header.Get("X-Signature") != v.want {
		return ErrInvalidSignature
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

// countingBeginner records transaction starts and always fails them.
type countingBeginner struct {
	begins atomic.Int32
}

func (c *countingBeginner) Begin(context.Context) (pgx.Tx, error) {
	c.begins.Add(1)
	return nil, errors.New("database unavailable")
}

func (c *countingBeginner) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	c.begins.Add(1)
	return nil, errors.New("database unavailable")
}

type noJobs struct{}

func (noJobs) InsertTx(context.Context, pgx.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, errors.New("must not be called")
}

func newIngest(t *testing.T, db *countingBeginner, maxBody int64) http.Handler {
	t.Helper()
	handler, err := Ingest(Config{
		DB:   db,
		Jobs: noJobs{},
		Sources: map[string]Source{
			"good": {Verifier: headerVerifier{want: "secret"}, ExternalID: idFromBody},
		},
		MaxBodyBytes: maxBody,
		Logger:       slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	router := httpx.NewRouter()
	router.Handle("POST /v1/webhooks/{source}", handler)
	return httpx.Chain(router, httpx.RequestID())
}

func deliver(h http.Handler, source, signature, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/webhooks/"+source, strings.NewReader(body))
	if signature != "" {
		req.Header.Set("X-Signature", signature)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestIngestRejectsBeforeTouchingTheDatabase(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		signature  string
		body       string
		wantStatus int
	}{
		{"unknown source", "nope", "secret", `{"id":"e1"}`, http.StatusNotFound},
		{"bad signature", "good", "wrong", `{"id":"e1"}`, http.StatusForbidden},
		{"missing signature", "good", "", `{"id":"e1"}`, http.StatusForbidden},
		{"body too large", "good", "secret", `{"id":"` + strings.Repeat("x", 600) + `"}`, http.StatusRequestEntityTooLarge},
		{"not json", "good", "secret", `not json`, http.StatusBadRequest},
		{"missing event id", "good", "secret", `{"type":"x"}`, http.StatusBadRequest},
		{"overlong event id", "good", "secret", `{"id":"` + strings.Repeat("x", 256) + `"}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := &countingBeginner{}
			rr := deliver(newIngest(t, db, 512), tt.source, tt.signature, tt.body)

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if got := db.begins.Load(); got != 0 {
				t.Errorf("a transaction started %d times for a rejected delivery", got)
			}
		})
	}
}

func TestIngestAnswers503WhenPersistenceFails(t *testing.T) {
	db := &countingBeginner{}
	rr := deliver(newIngest(t, db, 0), "good", "secret", `{"id":"e1"}`)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 so the sender retries", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "database unavailable") {
		t.Errorf("response leaks the internal error: %s", rr.Body.String())
	}
	if got := db.begins.Load(); got != 1 {
		t.Errorf("transaction starts = %d, want 1", got)
	}
}

func TestIngestValidatesItsConfig(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	if _, err := Ingest(Config{Jobs: noJobs{}, Logger: logger}); err == nil {
		t.Error("expected an error without a DB")
	}
	if _, err := Ingest(Config{DB: &countingBeginner{}, Logger: logger}); err == nil {
		t.Error("expected an error without a job inserter")
	}
	if _, err := Ingest(Config{DB: &countingBeginner{}, Jobs: noJobs{}}); err == nil {
		t.Error("expected an error without a logger")
	}
}

type stubQueryer struct {
	err error
}

func (s stubQueryer) QueryRow(context.Context, string, ...any) pgx.Row {
	return stubRow(s)
}

type stubRow struct {
	err error
}

func (r stubRow) Scan(...any) error { return r.err }

func TestWorkerCancelsWhenThereIsNothingToDo(t *testing.T) {
	called := false
	processors := map[string]Processor{
		"good": ProcessorFunc(func(context.Context, Event) error {
			called = true
			return nil
		}),
	}
	job := func(source string) *river.Job[EventArgs] {
		return &river.Job[EventArgs]{Args: EventArgs{EventID: 1, Source: source}}
	}

	unknown := &Worker{db: stubQueryer{}, processors: processors}
	if err := unknown.Work(context.Background(), job("other")); err == nil {
		t.Error("a source without a processor must cancel the job")
	}

	missing := &Worker{db: stubQueryer{err: pgx.ErrNoRows}, processors: processors}
	if err := missing.Work(context.Background(), job("good")); err == nil {
		t.Error("a missing event must cancel the job")
	}

	broken := &Worker{db: stubQueryer{err: errors.New("db down")}, processors: processors}
	if err := broken.Work(context.Background(), job("good")); err == nil || !strings.Contains(err.Error(), "db down") {
		t.Errorf("a load failure must be returned so River retries: %v", err)
	}
	if called {
		t.Error("the processor ran although no event was loaded")
	}
}

func TestEventArgsKind(t *testing.T) {
	if got := (EventArgs{}).Kind(); got != "webhook.event" {
		t.Errorf("Kind = %q, want webhook.event", got)
	}
}
