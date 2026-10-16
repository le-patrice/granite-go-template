package outbound

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

type reply struct {
	status int
	header http.Header
	err    error
}

// stubTransport replays canned replies and records what it was sent.
type stubTransport struct {
	mu      sync.Mutex
	replies []reply
	calls   int
	bodies  []string
	headers []http.Header
}

func (s *stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	body := ""
	if req.Body != nil {
		raw, _ := io.ReadAll(req.Body)
		body = string(raw)
	}
	s.bodies = append(s.bodies, body)
	s.headers = append(s.headers, req.Header.Clone())
	r := s.replies[min(s.calls, len(s.replies)-1)]
	s.calls++
	if r.err != nil {
		return nil, r.err
	}
	return &http.Response{
		StatusCode: r.status,
		Header:     r.header,
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

func (s *stubTransport) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type clock struct {
	now time.Time
}

func (c *clock) Now() time.Time          { return c.now }
func (c *clock) Advance(d time.Duration) { c.now = c.now.Add(d) }

type harness struct {
	client *http.Client
	stub   *stubTransport
	sleeps *[]time.Duration
	clock  *clock
}

func newHarness(t *testing.T, replies []reply, mutate func(*Config)) harness {
	t.Helper()
	stub := &stubTransport{replies: replies}
	clk := &clock{now: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)}
	sleeps := &[]time.Duration{}
	cfg := Config{
		Timeout:       time.Second,
		MaxRetries:    3,
		BaseBackoff:   100 * time.Millisecond,
		MaxBackoff:    time.Second,
		MaxRetryAfter: 5 * time.Second,
		Breaker:       BreakerConfig{FailureThreshold: 100, OpenFor: 30 * time.Second},
		Transport:     stub,
		Now:           clk.Now,
		Sleep: func(_ context.Context, d time.Duration) error {
			*sleeps = append(*sleeps, d)
			return nil
		},
		Jitter: func() float64 { return 1 },
	}
	if mutate != nil {
		mutate(&cfg)
	}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return harness{client: client, stub: stub, sleeps: sleeps, clock: clk}
}

func (h harness) do(t *testing.T, method string, body io.Reader, mutate func(*http.Request)) (int, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, "http://target.test/x", body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if mutate != nil {
		mutate(req)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, nil
}

func status(t *testing.T, code int, err error) int {
	t.Helper()
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return code
}

func doErr(client *http.Client, req *http.Request) error {
	resp, err := client.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	return err
}

func TestRetriesTransientStatusesForIdempotentRequests(t *testing.T) {
	h := newHarness(t, []reply{{status: 503}, {status: 502}, {status: 200}}, nil)

	code, err := h.do(t, http.MethodGet, nil, nil)
	if got := status(t, code, err); got != 200 {
		t.Fatalf("status = %d, want 200 after retries", got)
	}
	if got := h.stub.callCount(); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}
	if len(*h.sleeps) != 2 || (*h.sleeps)[0] != want[0] || (*h.sleeps)[1] != want[1] {
		t.Errorf("backoff = %v, want %v (exponential, full jitter at 1.0)", *h.sleeps, want)
	}
}

func TestBackoffIsJitteredAndCapped(t *testing.T) {
	h := newHarness(t, []reply{{status: 503}}, func(c *Config) {
		c.MaxRetries = 6
		c.Jitter = func() float64 { return 0.5 }
	})

	code, err := h.do(t, http.MethodGet, nil, nil)
	status(t, code, err)

	for i, d := range *h.sleeps {
		if d > 500*time.Millisecond {
			t.Errorf("sleep %d = %v, want at most half the 1s cap with jitter 0.5", i, d)
		}
	}
	if got := (*h.sleeps)[len(*h.sleeps)-1]; got != 500*time.Millisecond {
		t.Errorf("last sleep = %v, want 500ms (capped)", got)
	}
}

func TestPostIsNotRetriedUnlessItCarriesAnIdempotencyKey(t *testing.T) {
	plain := newHarness(t, []reply{{status: 503}, {status: 200}}, nil)
	code, err := plain.do(t, http.MethodPost, strings.NewReader("x"), nil)
	if got := status(t, code, err); got != 503 {
		t.Errorf("plain POST status = %d, want 503 with no retry", got)
	}
	if got := plain.stub.callCount(); got != 1 {
		t.Errorf("plain POST calls = %d, want 1", got)
	}

	keyed := newHarness(t, []reply{{status: 503}, {status: 200}}, nil)
	code, err = keyed.do(t, http.MethodPost, strings.NewReader("payload"), func(r *http.Request) {
		r.Header.Set("Idempotency-Key", "k1")
	})
	if got := status(t, code, err); got != 200 {
		t.Errorf("keyed POST status = %d, want 200 after a retry", got)
	}
	if keyed.stub.bodies[0] != "payload" || keyed.stub.bodies[1] != "payload" {
		t.Errorf("bodies = %v, want the body replayed", keyed.stub.bodies)
	}
	if keyed.stub.headers[1].Get("Idempotency-Key") != "k1" {
		t.Error("the retry lost the Idempotency-Key header")
	}
}

func TestBodylessRetryNeedsAReplayableBody(t *testing.T) {
	h := newHarness(t, []reply{{status: 503}, {status: 200}}, nil)

	code, err := h.do(t, http.MethodPut, io.NopCloser(strings.NewReader("one-shot")), nil)
	if got := status(t, code, err); got != 503 {
		t.Errorf("status = %d, want 503: a body that cannot be replayed must not be retried", got)
	}
	if got := h.stub.callCount(); got != 1 {
		t.Errorf("calls = %d, want 1", got)
	}
}

func TestRetriesTransportErrors(t *testing.T) {
	h := newHarness(t, []reply{{err: errors.New("connection reset")}, {err: errors.New("connection reset")}, {status: 200}}, nil)

	code, err := h.do(t, http.MethodGet, nil, nil)
	if got := status(t, code, err); got != 200 {
		t.Errorf("status = %d, want 200", got)
	}
	if got := h.stub.callCount(); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}

func TestOtherStatusesAreNotRetried(t *testing.T) {
	for _, wantCode := range []int{400, 404, 500} {
		h := newHarness(t, []reply{{status: wantCode}, {status: 200}}, nil)
		code, err := h.do(t, http.MethodGet, nil, nil)
		if got := status(t, code, err); got != wantCode {
			t.Errorf("status = %d, want %d", got, wantCode)
		}
		if got := h.stub.callCount(); got != 1 {
			t.Errorf("status %d: calls = %d, want 1", wantCode, got)
		}
	}
}

func TestRetryAfterIsHonored(t *testing.T) {
	h := newHarness(t, []reply{
		{status: 429, header: http.Header{"Retry-After": {"3"}}},
		{status: 503, header: http.Header{"Retry-After": {}}},
		{status: 200},
	}, nil)
	h.stub.replies[1].header.Set("Retry-After", h.clock.Now().Add(2*time.Second).UTC().Format(http.TimeFormat))

	code, err := h.do(t, http.MethodGet, nil, nil)
	if got := status(t, code, err); got != 200 {
		t.Fatalf("status = %d, want 200", got)
	}
	if len(*h.sleeps) != 2 || (*h.sleeps)[0] != 3*time.Second || (*h.sleeps)[1] != 2*time.Second {
		t.Errorf("sleeps = %v, want [3s 2s] from the seconds and HTTP-date forms", *h.sleeps)
	}
}

func TestRetryAfterBeyondTheCapIsReturnedWithoutRetrying(t *testing.T) {
	h := newHarness(t, []reply{{status: 429, header: http.Header{"Retry-After": {"60"}}}, {status: 200}}, nil)

	code, err := h.do(t, http.MethodGet, nil, nil)
	if got := status(t, code, err); got != 429 {
		t.Errorf("status = %d, want the 429 handed back", got)
	}
	if got := h.stub.callCount(); got != 1 || len(*h.sleeps) != 0 {
		t.Errorf("calls = %d, sleeps = %v; want one call and no sleep", got, *h.sleeps)
	}
}

func TestRetriesAreBounded(t *testing.T) {
	h := newHarness(t, []reply{{status: 503}}, nil)

	code, err := h.do(t, http.MethodGet, nil, nil)
	if got := status(t, code, err); got != 503 {
		t.Errorf("status = %d, want the last 503", got)
	}
	if got := h.stub.callCount(); got != 4 {
		t.Errorf("calls = %d, want 1 attempt plus 3 retries", got)
	}
}

func TestCallerCancellationStopsRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stub := &stubTransport{replies: []reply{{status: 503}}}
	client, err := NewClient(Config{
		MaxRetries: 3,
		Transport:  stub,
		Sleep: func(ctx context.Context, _ time.Duration) error {
			cancel()
			return ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://target.test/x", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if err := doErr(client, req); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if got := stub.callCount(); got != 1 {
		t.Errorf("calls = %d, want 1", got)
	}
}

type blockingTransport struct{}

func (blockingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func TestEachAttemptHasItsOwnTimeout(t *testing.T) {
	client, err := NewClient(Config{Timeout: 20 * time.Millisecond, Transport: blockingTransport{}})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://target.test/x", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	if err := doErr(client, req); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want a deadline error from the per-attempt timeout", err)
	}
}

func TestCircuitBreakerOpensProbesAndCloses(t *testing.T) {
	h := newHarness(t, []reply{{status: 503}, {status: 503}, {status: 503}, {status: 200}, {status: 200}}, func(c *Config) {
		c.MaxRetries = 0
		c.Breaker = BreakerConfig{FailureThreshold: 3, OpenFor: 30 * time.Second}
	})

	for i := 0; i < 3; i++ {
		code, err := h.do(t, http.MethodGet, nil, nil)
		if got := status(t, code, err); got != 503 {
			t.Fatalf("call %d status = %d, want 503", i+1, got)
		}
	}

	if _, err := h.do(t, http.MethodGet, nil, nil); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("err = %v, want ErrCircuitOpen once the threshold is reached", err)
	}
	if got := h.stub.callCount(); got != 3 {
		t.Errorf("calls = %d, want the open circuit to fail fast without a call", got)
	}

	h.clock.Advance(31 * time.Second)
	code, err := h.do(t, http.MethodGet, nil, nil)
	if got := status(t, code, err); got != 200 {
		t.Fatalf("probe status = %d, want 200", got)
	}
	code, err = h.do(t, http.MethodGet, nil, nil)
	if got := status(t, code, err); got != 200 {
		t.Errorf("status after the probe = %d, want the circuit closed", got)
	}
}

func TestFailedProbeReopensTheCircuit(t *testing.T) {
	h := newHarness(t, []reply{{status: 503}}, func(c *Config) {
		c.MaxRetries = 0
		c.Breaker = BreakerConfig{FailureThreshold: 1, OpenFor: 30 * time.Second}
	})

	code, err := h.do(t, http.MethodGet, nil, nil)
	status(t, code, err)
	if _, err := h.do(t, http.MethodGet, nil, nil); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("err = %v, want ErrCircuitOpen", err)
	}

	h.clock.Advance(31 * time.Second)
	code, err = h.do(t, http.MethodGet, nil, nil)
	status(t, code, err)
	if _, err := h.do(t, http.MethodGet, nil, nil); !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("err = %v, want the circuit open again after a failed probe", err)
	}
}

func TestHalfOpenAllowsOnlyOneProbe(t *testing.T) {
	clk := &clock{now: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)}
	b := &breaker{cfg: BreakerConfig{FailureThreshold: 1, OpenFor: time.Minute}, now: clk.Now}

	if err := b.allow(); err != nil {
		t.Fatalf("closed circuit rejected a call: %v", err)
	}
	b.failure()
	if err := b.allow(); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("err = %v, want open", err)
	}
	clk.Advance(2 * time.Minute)
	if err := b.allow(); err != nil {
		t.Fatalf("the first probe was rejected: %v", err)
	}
	if err := b.allow(); !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("a second concurrent probe err = %v, want rejection", err)
	}
	b.cancel()
	if err := b.allow(); err != nil {
		t.Errorf("after an abandoned probe the next call should be the probe: %v", err)
	}
}

func TestLimiterWaitHonorsTheCallersContext(t *testing.T) {
	limiter := rate.NewLimiter(rate.Every(time.Hour), 1)
	h := newHarness(t, []reply{{status: 200}}, func(c *Config) { c.Limiter = limiter })

	code, err := h.do(t, http.MethodGet, nil, nil)
	if got := status(t, code, err); got != 200 {
		t.Fatalf("first call status = %d", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://target.test/x", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if err := doErr(h.client, req); err == nil {
		t.Error("the second call went through although the limiter had no token for the whole deadline")
	}
	if got := h.stub.callCount(); got != 1 {
		t.Errorf("calls = %d, want 1", got)
	}
}

func TestNewClientRejectsNegativeSettings(t *testing.T) {
	for name, cfg := range map[string]Config{
		"retries": {MaxRetries: -1},
		"timeout": {Timeout: -time.Second},
		"backoff": {BaseBackoff: -time.Second},
	} {
		if _, err := NewClient(cfg); err == nil {
			t.Errorf("%s: expected an error for a negative setting", name)
		}
	}
}
