package httpx

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthz(t *testing.T) {
	rr := httptest.NewRecorder()
	Healthz().ServeHTTP(rr, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"ok"`) {
		t.Errorf("body = %s", rr.Body.String())
	}
}

func TestReadyz(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	passing := func(context.Context) error { return nil }
	failing := func(context.Context) error { return errors.New("pq: refused by 10.1.2.3") }

	tests := []struct {
		name       string
		checks     []Check
		wantStatus int
	}{
		{"no checks is ready", nil, http.StatusOK},
		{"passing checks are ready", []Check{passing, passing}, http.StatusOK},
		{"one failing check is not ready", []Check{passing, failing}, http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			Readyz(logger, time.Second, tt.checks...).ServeHTTP(rr, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil))

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if strings.Contains(rr.Body.String(), "10.1.2.3") {
				t.Errorf("body leaks check error: %s", rr.Body.String())
			}
		})
	}
}

func TestReadyzChecksReceiveTimeout(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	slow := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}

	rr := httptest.NewRecorder()
	Readyz(logger, 20*time.Millisecond, slow).ServeHTTP(rr, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when a check outlives the timeout", rr.Code)
	}
}
