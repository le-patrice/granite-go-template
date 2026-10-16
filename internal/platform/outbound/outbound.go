// Package outbound builds http.Clients for calling one external target with
// per-attempt timeouts, bounded jittered retries for idempotent calls,
// Retry-After handling, a circuit breaker, and an optional rate limiter.
package outbound

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ErrCircuitOpen is returned without contacting the target while the circuit is open.
var ErrCircuitOpen = errors.New("outbound: circuit breaker is open")

const (
	defaultTimeout          = 10 * time.Second
	defaultBaseBackoff      = 100 * time.Millisecond
	defaultMaxBackoff       = 2 * time.Second
	defaultMaxRetryAfter    = 10 * time.Second
	defaultFailureThreshold = 5
	defaultOpenFor          = 30 * time.Second
	drainLimit              = 4 << 10
)

// BreakerConfig configures the circuit breaker.
type BreakerConfig struct {
	// FailureThreshold is how many consecutive failures open the circuit.
	FailureThreshold int
	// OpenFor is how long the circuit stays open before one probe is allowed.
	OpenFor time.Duration
}

// Config configures NewClient. Zero values take the defaults noted below.
type Config struct {
	// Timeout bounds each attempt (default 10s).
	Timeout time.Duration
	// MaxRetries is the number of retries after the first attempt (default 0).
	MaxRetries int
	// BaseBackoff and MaxBackoff shape the exponential backoff (100ms, 2s).
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
	// MaxRetryAfter is the longest Retry-After that is honored (default 10s).
	MaxRetryAfter time.Duration
	Breaker       BreakerConfig
	// Limiter, when set, is waited on before every attempt.
	Limiter *rate.Limiter
	// Transport defaults to http.DefaultTransport.
	Transport http.RoundTripper

	// Now, Sleep, and Jitter are test seams.
	Now    func() time.Time
	Sleep  func(ctx context.Context, d time.Duration) error
	Jitter func() float64
}

// NewClient returns a client for one target.
func NewClient(cfg Config) (*http.Client, error) {
	if cfg.MaxRetries < 0 || cfg.Timeout < 0 || cfg.BaseBackoff < 0 || cfg.MaxBackoff < 0 {
		return nil, errors.New("outbound: negative settings are not allowed")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.BaseBackoff == 0 {
		cfg.BaseBackoff = defaultBaseBackoff
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = defaultMaxBackoff
	}
	if cfg.MaxRetryAfter == 0 {
		cfg.MaxRetryAfter = defaultMaxRetryAfter
	}
	if cfg.Breaker.FailureThreshold == 0 {
		cfg.Breaker.FailureThreshold = defaultFailureThreshold
	}
	if cfg.Breaker.OpenFor == 0 {
		cfg.Breaker.OpenFor = defaultOpenFor
	}
	if cfg.Transport == nil {
		cfg.Transport = http.DefaultTransport
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepContext
	}
	if cfg.Jitter == nil {
		cfg.Jitter = rand.Float64
	}
	return &http.Client{
		Transport: &roundTripper{cfg: cfg, breaker: &breaker{cfg: cfg.Breaker, now: cfg.Now}},
	}, nil
}

type roundTripper struct {
	cfg     Config
	breaker *breaker
}

func (rt *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	canRetry := rt.cfg.MaxRetries > 0 && idempotent(req) && replayable(req)
	current := req

	for attempt := 0; ; attempt++ {
		resp, err := rt.once(ctx, current)
		if ctx.Err() != nil || !canRetry || attempt >= rt.cfg.MaxRetries || errors.Is(err, ErrCircuitOpen) {
			return resp, err
		}
		delay, retry := rt.retryDelay(attempt, resp, err)
		if !retry {
			return resp, err
		}
		if resp != nil {
			drain(resp)
		}
		next, rewindErr := rewind(current)
		if rewindErr != nil {
			return nil, rewindErr
		}
		if sleepErr := rt.cfg.Sleep(ctx, delay); sleepErr != nil {
			return nil, sleepErr
		}
		current = next
	}
}

// once makes one attempt: limiter, breaker, then the call under its own timeout.
func (rt *roundTripper) once(ctx context.Context, req *http.Request) (*http.Response, error) {
	if rt.cfg.Limiter != nil {
		if err := rt.cfg.Limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("outbound: rate limit: %w", err)
		}
	}
	if err := rt.breaker.allow(); err != nil {
		return nil, err
	}

	attemptCtx, cancel := context.WithTimeout(ctx, rt.cfg.Timeout)
	resp, err := rt.cfg.Transport.RoundTrip(req.WithContext(attemptCtx))
	switch {
	case err != nil:
		cancel()
		if ctx.Err() != nil {
			rt.breaker.cancel()
		} else {
			rt.breaker.failure()
		}
		return nil, err
	case resp.StatusCode >= http.StatusInternalServerError:
		rt.breaker.failure()
	default:
		rt.breaker.success()
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

func (rt *roundTripper) retryDelay(attempt int, resp *http.Response, err error) (time.Duration, bool) {
	if err != nil {
		return rt.backoff(attempt), true
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		if wait, ok := parseRetryAfter(resp.Header.Get("Retry-After"), rt.cfg.Now()); ok {
			if wait > rt.cfg.MaxRetryAfter {
				return 0, false
			}
			return wait, true
		}
		return rt.backoff(attempt), true
	case http.StatusBadGateway, http.StatusGatewayTimeout:
		return rt.backoff(attempt), true
	default:
		return 0, false
	}
}

// backoff is exponential with full jitter: a random delay in [0, min(max, base*2^attempt)).
func (rt *roundTripper) backoff(attempt int) time.Duration {
	ceiling := rt.cfg.BaseBackoff << attempt
	if ceiling <= 0 || ceiling > rt.cfg.MaxBackoff {
		ceiling = rt.cfg.MaxBackoff
	}
	return time.Duration(rt.cfg.Jitter() * float64(ceiling))
}

func idempotent(req *http.Request) bool {
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete, http.MethodTrace:
		return true
	default:
		return req.Header.Get("Idempotency-Key") != ""
	}
}

func replayable(req *http.Request) bool {
	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}

func rewind(req *http.Request) (*http.Request, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return req, nil
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, fmt.Errorf("outbound: replay request body: %w", err)
	}
	next := req.Clone(req.Context())
	next.Body = body
	return next, nil
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second, true
	}
	if at, err := http.ParseTime(value); err == nil {
		if wait := at.Sub(now); wait > 0 {
			return wait, true
		}
		return 0, true
	}
	return 0, false
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, drainLimit))
	_ = resp.Body.Close()
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// cancelOnClose releases the attempt's timeout when the caller closes the body.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

type breakerState int

const (
	stateClosed breakerState = iota
	stateOpen
	stateHalfOpen
)

// breaker is a consecutive-failure circuit breaker with a single half-open probe.
type breaker struct {
	cfg      BreakerConfig
	now      func() time.Time
	mu       sync.Mutex
	state    breakerState
	failures int
	openedAt time.Time
	probing  bool
}

func (b *breaker) allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case stateOpen:
		if b.now().Sub(b.openedAt) < b.cfg.OpenFor {
			return ErrCircuitOpen
		}
		b.state = stateHalfOpen
		b.probing = true
	case stateHalfOpen:
		if b.probing {
			return ErrCircuitOpen
		}
		b.probing = true
	case stateClosed:
	}
	return nil
}

func (b *breaker) success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = stateClosed
	b.failures = 0
	b.probing = false
}

func (b *breaker) failure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probing = false
	if b.state == stateHalfOpen {
		b.trip()
		return
	}
	b.failures++
	if b.failures >= b.cfg.FailureThreshold {
		b.trip()
	}
}

// cancel frees a half-open probe without a verdict, for attempts the caller abandoned.
func (b *breaker) cancel() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probing = false
}

func (b *breaker) trip() {
	b.state = stateOpen
	b.openedAt = b.now()
	b.failures = 0
}
