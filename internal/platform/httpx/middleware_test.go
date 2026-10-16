package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestRecoverReturnsGenericProblemAndLogsStack(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	h := Chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("boom: secret internal detail")
		}),
		Recover(logger),
		RequestID(),
	)

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, "req-123")
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q", got)
	}
	if strings.Contains(rr.Body.String(), "secret") {
		t.Errorf("response leaks panic text: %s", rr.Body.String())
	}
	var p Problem
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if p.RequestID != "req-123" {
		t.Errorf("problem request_id = %q, want req-123", p.RequestID)
	}

	out := logs.String()
	for _, want := range []string{"panic recovered", "req-123", "stack", "secret internal detail"} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q: %s", want, out)
		}
	}
}

func TestRecoverKeepsAlreadyWrittenResponse(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	h := Recover(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		panic("late failure")
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))

	if rr.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202 (response already started)", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rr.Body.String())
	}
}

func TestRecoverReraisesAbortHandler(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	h := Recover(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		if recover() == nil {
			t.Error("http.ErrAbortHandler must propagate")
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))
}

func TestRequestID(t *testing.T) {
	generated := regexp.MustCompile(`^[0-9a-f]{32}$`)
	tests := []struct {
		name     string
		inbound  string
		wantSame bool
	}{
		{"keeps a valid inbound id", "abc-123_x.y", true},
		{"replaces an id with spaces", "bad id", false},
		{"replaces an overlong id", strings.Repeat("a", 65), false},
		{"generates when absent", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var inContext string
			h := RequestID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				inContext = RequestIDFrom(r.Context())
			}))
			rr := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			if tt.inbound != "" {
				req.Header.Set(RequestIDHeader, tt.inbound)
			}
			h.ServeHTTP(rr, req)

			header := rr.Header().Get(RequestIDHeader)
			if header == "" || header != inContext {
				t.Fatalf("header %q and context %q must match and be non-empty", header, inContext)
			}
			if tt.wantSame && header != tt.inbound {
				t.Errorf("id = %q, want %q", header, tt.inbound)
			}
			if !tt.wantSame && !generated.MatchString(header) {
				t.Errorf("id = %q, want 32 hex characters", header)
			}
		})
	}
}

func TestChainOrder(t *testing.T) {
	var order []string
	mark := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		order = append(order, "handler")
	}), mark("first"), mark("second"), mark("third"))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))

	if got := strings.Join(order, ","); got != "first,second,third,handler" {
		t.Errorf("order = %s, want first,second,third,handler", got)
	}
}
