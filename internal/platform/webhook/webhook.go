// Package webhook is the inbound webhook kit. Ingest verifies a delivery,
// stores it once, and enqueues a job in the same transaction. Worker later
// hands each stored event to the Processor registered for its source.
package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"example.com/app/internal/platform/httpx"
	"example.com/app/internal/platform/store"
)

const (
	// KindEvent is the River job kind enqueued for every stored event.
	KindEvent = "webhook.event"

	defaultMaxBody   = 1 << 20
	maxExternalIDLen = 255

	insertEventSQL = `INSERT INTO inbound_events (source, external_id, payload)
VALUES ($1, $2, $3)
ON CONFLICT (source, external_id) DO NOTHING
RETURNING id`
	selectEventSQL = `SELECT id, source, external_id, payload FROM inbound_events WHERE id = $1`
)

// ErrInvalidSignature is what Verifier implementations return for a bad signature.
var ErrInvalidSignature = errors.New("webhook: invalid signature")

// Verifier authenticates a delivery. Implementations must compare signatures
// in constant time (hmac.Equal) and return ErrInvalidSignature on mismatch.
type Verifier interface {
	Verify(body []byte, header http.Header) error
}

// Source describes one sender.
type Source struct {
	Verifier Verifier
	// ExternalID extracts the sender's unique event ID from a verified body.
	ExternalID func(body []byte) (string, error)
}

// JobInserter enqueues a job in an existing transaction. The insert-only
// *river.Client[pgx.Tx] satisfies it.
type JobInserter interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// Config configures Ingest.
type Config struct {
	DB      store.TxBeginner
	Jobs    JobInserter
	Sources map[string]Source
	// MaxBodyBytes caps the request body. It defaults to 1 MiB.
	MaxBodyBytes int64
	Logger       *slog.Logger
}

// Ingest returns the handler for POST /v1/webhooks/{source}. Register it with
// httpx.Router so the {source} path value is available.
func Ingest(cfg Config) (http.Handler, error) {
	if cfg.DB == nil || cfg.Jobs == nil || cfg.Logger == nil {
		return nil, errors.New("webhook: DB, Jobs, and Logger are required")
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = defaultMaxBody
	}
	return &ingestor{cfg: cfg}, nil
}

type ingestor struct {
	cfg Config
}

func (i *ingestor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("source")
	source, ok := i.cfg.Sources[name]
	if !ok {
		httpx.WriteProblem(w, r, http.StatusNotFound, "unknown webhook source")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, i.cfg.MaxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpx.WriteProblem(w, r, http.StatusRequestEntityTooLarge, "request body is too large")
			return
		}
		httpx.WriteProblem(w, r, http.StatusBadRequest, "request body could not be read")
		return
	}

	if err := source.Verifier.Verify(body, r.Header); err != nil {
		httpx.WriteProblem(w, r, http.StatusForbidden, "invalid signature")
		return
	}

	externalID, err := source.ExternalID(body)
	if err != nil || externalID == "" || len(externalID) > maxExternalIDLen || !json.Valid(body) {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "malformed event")
		return
	}

	if err := i.persist(r.Context(), name, externalID, body); err != nil {
		i.cfg.Logger.ErrorContext(r.Context(), "webhook persistence failed",
			"source", name,
			"error", err,
			"request_id", httpx.RequestIDFrom(r.Context()),
		)
		httpx.WriteProblem(w, r, http.StatusServiceUnavailable, "")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `{"status":"ok"}`+"\n")
}

// persist stores the event and enqueues its job in one transaction. A duplicate
// delivery stores and enqueues nothing and is not an error.
func (i *ingestor) persist(ctx context.Context, source, externalID string, body []byte) error {
	return store.InTx(ctx, i.cfg.DB, func(ctx context.Context, tx pgx.Tx) error {
		var id int64
		err := tx.QueryRow(ctx, insertEventSQL, source, externalID, json.RawMessage(body)).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("insert inbound event: %w", err)
		}
		if _, err := i.cfg.Jobs.InsertTx(ctx, tx, EventArgs{EventID: id, Source: source}, nil); err != nil {
			return fmt.Errorf("enqueue %s: %w", KindEvent, err)
		}
		return nil
	})
}

// EventArgs are the arguments of a webhook.event job.
type EventArgs struct {
	EventID int64  `json:"event_id"`
	Source  string `json:"source"`
}

// Kind implements river.JobArgs.
func (EventArgs) Kind() string { return KindEvent }

// Event is a stored inbound event.
type Event struct {
	ID         int64
	Source     string
	ExternalID string
	Payload    json.RawMessage
}

// Processor handles one stored event. Jobs run at least once, so Process must
// be idempotent: key any side effect on Event.ExternalID.
type Processor interface {
	Process(ctx context.Context, event Event) error
}

// ProcessorFunc adapts a function to Processor.
type ProcessorFunc func(ctx context.Context, event Event) error

// Process calls f.
func (f ProcessorFunc) Process(ctx context.Context, event Event) error { return f(ctx, event) }

// LogProcessor returns a Processor that logs the event's identity and size,
// never its payload.
func LogProcessor(logger *slog.Logger) Processor {
	return ProcessorFunc(func(ctx context.Context, event Event) error {
		logger.InfoContext(ctx, "webhook event received",
			"source", event.Source,
			"external_id", event.ExternalID,
			"payload_bytes", len(event.Payload),
		)
		return nil
	})
}

// Queryer reads one row. *pgxpool.Pool satisfies it.
type Queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Worker loads a stored event and dispatches it to its source's Processor.
type Worker struct {
	river.WorkerDefaults[EventArgs]
	db         Queryer
	processors map[string]Processor
}

// Work loads the event and runs its processor. A missing event or a source
// without a processor cancels the job, since retrying cannot help.
func (w *Worker) Work(ctx context.Context, job *river.Job[EventArgs]) error {
	processor, ok := w.processors[job.Args.Source]
	if !ok {
		return river.JobCancel(fmt.Errorf("webhook: no processor for source %q", job.Args.Source))
	}

	var (
		event   Event
		payload []byte
	)
	err := w.db.QueryRow(ctx, selectEventSQL, job.Args.EventID).Scan(&event.ID, &event.Source, &event.ExternalID, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return river.JobCancel(fmt.Errorf("webhook: event %d no longer exists", job.Args.EventID))
	}
	if err != nil {
		return fmt.Errorf("webhook: load event %d: %w", job.Args.EventID, err)
	}
	event.Payload = json.RawMessage(payload)

	if err := processor.Process(ctx, event); err != nil {
		return fmt.Errorf("webhook: process event %d: %w", event.ID, err)
	}
	return nil
}

// RegisterWorker adds the webhook worker to the bundle. processors maps a
// source name to its Processor and may be empty.
func RegisterWorker(workers *river.Workers, db Queryer, processors map[string]Processor) {
	river.AddWorker(workers, &Worker{db: db, processors: processors})
}
