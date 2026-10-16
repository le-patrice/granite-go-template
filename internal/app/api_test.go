package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"example.com/app/internal/platform/httpx"
	"example.com/app/internal/platform/ratelimit"
	"example.com/app/internal/platform/telemetry"
)

type testChain struct {
	handler http.Handler
	metrics *httpx.Metrics
	limiter *ratelimit.Limiter
}

func newTestChain(t *testing.T, burst, maxKeys int, trusted ...string) testChain {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	prov, err := telemetry.Setup(context.Background(), telemetry.Options{ServiceName: "test"})
	if err != nil {
		t.Fatalf("telemetry.Setup: %v", err)
	}
	proxies, err := httpx.ParseTrustedProxies(trusted)
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}
	metrics, err := httpx.NewMetrics()
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	limiter, err := ratelimit.New(ratelimit.Config{
		Rate:    rate.Limit(1.0 / 60),
		Burst:   burst,
		IdleTTL: time.Hour,
		MaxKeys: maxKeys,
	})
	if err != nil {
		t.Fatalf("ratelimit.New: %v", err)
	}

	router := httpx.NewRouter()
	router.HandleFunc("GET /ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := newChain(router, chainDeps{
		logger:  logger,
		proxies: proxies,
		prov:    prov,
		metrics: metrics,
		limiter: limiter,
	})
	return testChain{handler: handler, metrics: metrics, limiter: limiter}
}

func (c testChain) hit(remote, forwardedFor string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ping", nil)
	req.RemoteAddr = remote
	if forwardedFor != "" {
		req.Header.Set("X-Forwarded-For", forwardedFor)
	}
	rr := httptest.NewRecorder()
	c.handler.ServeHTTP(rr, req)
	return rr
}

func TestGeneralRateLimitReturns429WithRetryAfter(t *testing.T) {
	c := newTestChain(t, 2, 100)

	for i := 1; i <= 2; i++ {
		if rr := c.hit("192.0.2.1:1000", ""); rr.Code != http.StatusNoContent {
			t.Fatalf("request %d status = %d, want 204", i, rr.Code)
		}
	}
	rr := c.hit("192.0.2.1:1001", "")
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("third request status = %d, want 429", rr.Code)
	}
	if got := rr.Header().Get("Retry-After"); got != "60" {
		t.Errorf("Retry-After = %q, want 60", got)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}
	if rr.Header().Get(httpx.RequestIDHeader) == "" {
		t.Error("a limited response has no request ID: the limiter sits outside the request-ID middleware")
	}
	if other := c.hit("192.0.2.2:1000", ""); other.Code != http.StatusNoContent {
		t.Errorf("a different client was limited: %d", other.Code)
	}
}

func TestForgedForwardedForDoesNotEscapeTheLimit(t *testing.T) {
	c := newTestChain(t, 1, 100)

	if rr := c.hit("192.0.2.1:1000", "203.0.113.1"); rr.Code != http.StatusNoContent {
		t.Fatalf("first status = %d", rr.Code)
	}
	for _, forged := range []string{"203.0.113.2", "203.0.113.3, 198.51.100.9"} {
		if rr := c.hit("192.0.2.1:1000", forged); rr.Code != http.StatusTooManyRequests {
			t.Errorf("a client behind an untrusted peer evaded the limit with X-Forwarded-For %q: %d", forged, rr.Code)
		}
	}
}

func TestTrustedProxyClientsGetSeparateBuckets(t *testing.T) {
	c := newTestChain(t, 1, 100, "10.0.0.0/8")

	if rr := c.hit("10.0.0.2:1000", "203.0.113.1"); rr.Code != http.StatusNoContent {
		t.Fatalf("client one first request = %d", rr.Code)
	}
	if rr := c.hit("10.0.0.2:1001", "203.0.113.1"); rr.Code != http.StatusTooManyRequests {
		t.Errorf("client one second request = %d, want 429", rr.Code)
	}
	if rr := c.hit("10.0.0.2:1002", "203.0.113.2"); rr.Code != http.StatusNoContent {
		t.Errorf("client two was limited by client one's bucket: %d", rr.Code)
	}
}

func TestLimitedRequestsAreCountedInMetrics(t *testing.T) {
	c := newTestChain(t, 1, 100)
	c.hit("192.0.2.1:1000", "")
	c.hit("192.0.2.1:1001", "")

	rr := httptest.NewRecorder()
	c.metrics.Handler().ServeHTTP(rr, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil))
	if !strings.Contains(rr.Body.String(), `status="429"`) {
		t.Errorf("metrics do not record the 429: %s", rr.Body.String())
	}
}

func TestLimiterMemoryStaysBoundedUnderManyClients(t *testing.T) {
	const maxKeys = 5
	c := newTestChain(t, 1, maxKeys)

	for i := 0; i < 500; i++ {
		c.hit("198.51.100."+strconv.Itoa(i%250)+":"+strconv.Itoa(1000+i), "")
		if got := c.limiter.Len(); got > maxKeys {
			t.Fatalf("limiter tracks %d keys after %d requests, want at most %d", got, i+1, maxKeys)
		}
	}
}
