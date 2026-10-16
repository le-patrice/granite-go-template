// Package idempotency makes POST endpoints safe to retry. A client sends an
// Idempotency-Key; the first request runs and its response is stored, repeats
// get the stored response, and conflicting reuse is rejected.
package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"example.com/app/internal/platform/httpx"
)

const (
	// Header is the request header that carries the client's idempotency key.
	Header = "Idempotency-Key"
	// ReplayedHeader is set to "true" on a response served from the store.
	ReplayedHeader = "Idempotent-Replayed"

	maxKeyLength   = 255
	defaultMaxBody = 1 << 20
	storeTimeout   = 5 * time.Second
)

// ErrNotFound is returned by Store.Get for an unknown key.
var ErrNotFound = errors.New("idempotency: key not found")

// Record is the stored state of a key.
type Record struct {
	RequestHash []byte
	// Status is 0 while the first request is still in flight.
	Status int
	// Body is the stored response envelope.
	Body []byte
}

// Store persists idempotency keys.
type Store interface {
	// Reserve claims (scope, key) for a new request. It returns false when the
	// key already exists. It must be atomic.
	Reserve(ctx context.Context, scope, key string, requestHash []byte) (bool, error)
	Get(ctx context.Context, scope, key string) (Record, error)
	Complete(ctx context.Context, scope, key string, status int, body []byte) error
	Release(ctx context.Context, scope, key string) error
	DeleteExpired(ctx context.Context, cutoff time.Time) (int64, error)
}

// Config configures Middleware.
type Config struct {
	Store Store
	// Scope returns the scope of the request, normally the authenticated user
	// ID. An empty scope means the caller is not authenticated.
	Scope func(*http.Request) string
	// MaxBodyBytes caps both the request body and the stored response.
	MaxBodyBytes int64
	Logger       *slog.Logger
}

// Middleware returns the idempotency middleware. Apply it after authentication.
func Middleware(cfg Config) (func(http.Handler) http.Handler, error) {
	if cfg.Store == nil || cfg.Scope == nil {
		return nil, errors.New("idempotency: store and scope are required")
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = defaultMaxBody
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	return func(next http.Handler) http.Handler {
		return &guard{cfg: cfg, next: next}
	}, nil
}

type guard struct {
	cfg  Config
	next http.Handler
}

type envelope struct {
	Header map[string][]string `json:"header,omitempty"`
	Body   []byte              `json:"body,omitempty"`
}

func (g *guard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	scope := g.cfg.Scope(r)
	if scope == "" {
		httpx.WriteProblem(w, r, http.StatusUnauthorized, "authentication required")
		return
	}
	key := r.Header.Get(Header)
	if !validKey(key) {
		httpx.WriteProblem(w, r, http.StatusBadRequest,
			"Idempotency-Key header is required and must be 1 to 255 visible ASCII characters")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, g.cfg.MaxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpx.WriteProblem(w, r, http.StatusRequestEntityTooLarge, "request body is too large")
			return
		}
		httpx.WriteProblem(w, r, http.StatusBadRequest, "request body could not be read")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	hash := requestHash(r, body)

	reserved, err := g.cfg.Store.Reserve(r.Context(), scope, key, hash)
	if err != nil {
		g.fail(w, r, "reserve key", err)
		return
	}
	if !reserved {
		g.replayOrReject(w, r, scope, key, hash)
		return
	}
	g.execute(w, r, scope, key)
}

func (g *guard) replayOrReject(w http.ResponseWriter, r *http.Request, scope, key string, hash []byte) {
	rec, err := g.cfg.Store.Get(r.Context(), scope, key)
	switch {
	case errors.Is(err, ErrNotFound):
		w.Header().Set("Retry-After", "1")
		httpx.WriteProblem(w, r, http.StatusConflict, "the request with this Idempotency-Key was just released; retry")
	case err != nil:
		g.fail(w, r, "read key", err)
	case !bytes.Equal(rec.RequestHash, hash):
		httpx.WriteProblem(w, r, http.StatusUnprocessableEntity,
			"Idempotency-Key was already used with a different request")
	case rec.Status == 0:
		w.Header().Set("Retry-After", "1")
		httpx.WriteProblem(w, r, http.StatusConflict,
			"a request with this Idempotency-Key is still in progress")
	default:
		g.replay(w, r, rec)
	}
}

func (g *guard) replay(w http.ResponseWriter, r *http.Request, rec Record) {
	var stored envelope
	if err := json.Unmarshal(rec.Body, &stored); err != nil {
		g.fail(w, r, "decode stored response", err)
		return
	}
	for name, values := range stored.Header {
		w.Header()[name] = values
	}
	w.Header().Set(ReplayedHeader, "true")
	w.WriteHeader(rec.Status)
	_, _ = w.Write(stored.Body)
}

// execute runs the handler once and stores its response. If the handler
// panics, the key is released before the panic continues.
func (g *guard) execute(w http.ResponseWriter, r *http.Request, scope, key string) {
	ctx := r.Context()
	capture := &captureWriter{ResponseWriter: w, limit: int(g.cfg.MaxBodyBytes)}
	finished := false
	defer func() {
		if !finished {
			g.release(ctx, scope, key)
		}
	}()

	g.next.ServeHTTP(capture, r)
	finished = true
	g.finish(ctx, capture, scope, key)
}

func (g *guard) finish(ctx context.Context, capture *captureWriter, scope, key string) {
	status := capture.status
	if status == 0 {
		status = http.StatusOK
	}
	if status >= http.StatusInternalServerError || capture.overflow {
		g.release(ctx, scope, key)
		return
	}

	payload, err := json.Marshal(envelope{Header: keptHeaders(capture.header), Body: capture.body.Bytes()})
	if err != nil {
		g.cfg.Logger.ErrorContext(ctx, "idempotency: encode response", "error", err)
		g.release(ctx, scope, key)
		return
	}
	storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storeTimeout)
	defer cancel()
	if err := g.cfg.Store.Complete(storeCtx, scope, key, status, payload); err != nil {
		g.cfg.Logger.ErrorContext(ctx, "idempotency: store response", "error", err)
		g.release(ctx, scope, key)
	}
}

func (g *guard) release(ctx context.Context, scope, key string) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storeTimeout)
	defer cancel()
	if err := g.cfg.Store.Release(releaseCtx, scope, key); err != nil {
		g.cfg.Logger.ErrorContext(ctx, "idempotency: release key", "error", err)
	}
}

func (g *guard) fail(w http.ResponseWriter, r *http.Request, op string, err error) {
	g.cfg.Logger.ErrorContext(r.Context(), "idempotency: "+op,
		"error", err,
		"request_id", httpx.RequestIDFrom(r.Context()),
	)
	httpx.WriteInternal(w, r)
}

// captureWriter passes the response through while keeping a bounded copy.
type captureWriter struct {
	http.ResponseWriter
	status   int
	header   http.Header
	body     bytes.Buffer
	overflow bool
	limit    int
}

func (c *captureWriter) WriteHeader(code int) {
	if code >= 100 && code < 200 {
		c.ResponseWriter.WriteHeader(code)
		return
	}
	if c.status == 0 {
		c.status = code
		c.header = c.Header().Clone()
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *captureWriter) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.WriteHeader(http.StatusOK)
	}
	if !c.overflow {
		if c.body.Len()+len(b) > c.limit {
			c.overflow = true
			c.body.Reset()
		} else {
			c.body.Write(b)
		}
	}
	return c.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (c *captureWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func keptHeaders(h http.Header) map[string][]string {
	kept := make(map[string][]string)
	for _, name := range []string{"Content-Type", "Location"} {
		if values := h.Values(name); len(values) > 0 {
			kept[name] = values
		}
	}
	return kept
}

func requestHash(r *http.Request, body []byte) []byte {
	h := sha256.New()
	h.Write([]byte(r.Method))
	h.Write([]byte{'\n'})
	h.Write([]byte(r.URL.RequestURI()))
	h.Write([]byte{'\n'})
	h.Write(body)
	return h.Sum(nil)
}

func validKey(key string) bool {
	if key == "" || len(key) > maxKeyLength {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] <= 0x20 || key[i] >= 0x7f {
			return false
		}
	}
	return true
}

// Cleanup deletes keys older than ttl every interval until ctx is cancelled.
// Run it in the worker so abandoned in-flight rows do not accumulate.
func Cleanup(ctx context.Context, store Store, interval, ttl time.Duration, logger *slog.Logger) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweepCtx, cancel := context.WithTimeout(ctx, storeTimeout)
			deleted, err := store.DeleteExpired(sweepCtx, time.Now().UTC().Add(-ttl))
			cancel()
			if err != nil {
				logger.ErrorContext(ctx, "idempotency cleanup failed", "error", err)
				continue
			}
			if deleted > 0 {
				logger.InfoContext(ctx, "idempotency keys expired", "deleted", deleted)
			}
		}
	}
}
