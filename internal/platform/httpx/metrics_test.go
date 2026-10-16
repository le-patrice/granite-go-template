package httpx

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestMetricsLabelByRoutePatternNotRawPath(t *testing.T) {
	metrics, err := NewMetrics()
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	router := NewRouter()
	router.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	router.Handle("GET /metrics", metrics.Handler())

	h := Chain(router,
		Recover(logger),
		RequestID(),
		Telemetry(logger, noop.NewTracerProvider(), propagation.TraceContext{}),
		metrics.Middleware(),
	)

	for _, path := range []string{"/items/1", "/items/2", "/missing/abc"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	for _, want := range []string{
		`route="GET /items/{id}"`,
		`route="unmatched"`,
		`status="204"`,
		`status="404"`,
		"http_request_duration_seconds_bucket",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %s", want)
		}
	}
	for _, leaked := range []string{"/items/1", "/items/2", "/missing/abc"} {
		if strings.Contains(body, leaked) {
			t.Errorf("metrics output contains raw path %s", leaked)
		}
	}
}

func TestMetricsRecordPanicsAs500(t *testing.T) {
	metrics, err := NewMetrics()
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	router := NewRouter()
	router.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	router.Handle("GET /metrics", metrics.Handler())

	h := Chain(router,
		Recover(logger),
		RequestID(),
		Telemetry(logger, noop.NewTracerProvider(), propagation.TraceContext{}),
		metrics.Middleware(),
	)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/boom", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}

	scrape := httptest.NewRecorder()
	h.ServeHTTP(scrape, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil))
	if !strings.Contains(scrape.Body.String(), `route="GET /boom",status="500"`) {
		t.Errorf("panic was not counted as a 500: %s", scrape.Body.String())
	}
}
