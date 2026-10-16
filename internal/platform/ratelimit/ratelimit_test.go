package ratelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func newClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)}
}

func newLimiter(t *testing.T, clock *fakeClock, cfg Config) *Limiter {
	t.Helper()
	cfg.Now = clock.Now
	l, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return l
}

func TestAllowEnforcesBurstThenRefills(t *testing.T) {
	clock := newClock()
	l := newLimiter(t, clock, Config{Rate: 1, Burst: 3, IdleTTL: time.Hour, MaxKeys: 10})

	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("request %d inside the burst was denied", i+1)
		}
	}
	ok, retryAfter := l.Allow("a")
	if ok {
		t.Fatal("request beyond the burst was allowed")
	}
	if retryAfter <= 0 || retryAfter > time.Second {
		t.Errorf("retryAfter = %v, want within (0, 1s]", retryAfter)
	}

	clock.Advance(time.Second)
	if ok, _ := l.Allow("a"); !ok {
		t.Error("request after one refill interval was denied")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	l := newLimiter(t, newClock(), Config{Rate: 1, Burst: 1, IdleTTL: time.Hour, MaxKeys: 10})

	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("first request for a was denied")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Error("second request for a was allowed")
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Error("first request for b was denied because of a")
	}
}

func TestIdleEntriesAreEvicted(t *testing.T) {
	clock := newClock()
	l := newLimiter(t, clock, Config{Rate: 1, Burst: 1, IdleTTL: time.Minute, MaxKeys: 100})

	for _, key := range []string{"a", "b", "c"} {
		l.Allow(key)
	}
	if got := l.Len(); got != 3 {
		t.Fatalf("Len = %d, want 3", got)
	}

	clock.Advance(2 * time.Minute)
	l.Allow("fresh")

	if got := l.Len(); got != 1 {
		t.Errorf("Len = %d after idle eviction, want 1", got)
	}
}

func TestMaxKeysBoundsMemory(t *testing.T) {
	l := newLimiter(t, newClock(), Config{Rate: 1, Burst: 1, IdleTTL: time.Hour, MaxKeys: 10})

	for i := 0; i < 1000; i++ {
		l.Allow("key-" + strconv.Itoa(i))
		if got := l.Len(); got > 10 {
			t.Fatalf("Len = %d after %d keys, want at most 10", got, i+1)
		}
	}
}

func TestConcurrentAllowRespectsBurst(t *testing.T) {
	l := newLimiter(t, newClock(), Config{Rate: 0.001, Burst: 5, IdleTTL: time.Hour, MaxKeys: 10})

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := l.Allow("shared"); ok {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := allowed.Load(); got != 5 {
		t.Errorf("allowed = %d, want exactly the burst of 5", got)
	}
}

func TestNewValidatesConfig(t *testing.T) {
	good := Config{Rate: 1, Burst: 1, IdleTTL: time.Minute, MaxKeys: 1}
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"zero rate", func(c *Config) { c.Rate = 0 }},
		{"zero burst", func(c *Config) { c.Burst = 0 }},
		{"zero idle TTL", func(c *Config) { c.IdleTTL = 0 }},
		{"zero max keys", func(c *Config) { c.MaxKeys = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := good
			tt.mutate(&cfg)
			if _, err := New(cfg); err == nil {
				t.Error("expected an error")
			}
		})
	}
	if _, err := New(good); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}

func TestPerMinute(t *testing.T) {
	if got := PerMinute(60); got != rate.Limit(1) {
		t.Errorf("PerMinute(60) = %v, want 1 per second", got)
	}
}

func TestMiddlewareReturns429WithRetryAfter(t *testing.T) {
	l := newLimiter(t, newClock(), Config{Rate: PerMinute(1), Burst: 1, IdleTTL: time.Hour, MaxKeys: 10})
	handler := Middleware(l, ByClientIP)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	call := func(remoteAddr string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/auth/login", nil)
		req.RemoteAddr = remoteAddr
		handler.ServeHTTP(rr, req)
		return rr
	}

	if rr := call("192.0.2.1:1000"); rr.Code != http.StatusNoContent {
		t.Fatalf("first request status = %d, want 204", rr.Code)
	}
	rr := call("192.0.2.1:2000")
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", rr.Code)
	}
	if got := rr.Header().Get("Retry-After"); got != "60" {
		t.Errorf("Retry-After = %q, want 60", got)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}
	if rr := call("192.0.2.2:1000"); rr.Code != http.StatusNoContent {
		t.Errorf("a different client was limited: status = %d", rr.Code)
	}
}
