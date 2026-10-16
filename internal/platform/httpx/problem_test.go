package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteProblem(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		detail     string
		wantDetail string
	}{
		{"client error keeps detail", http.StatusBadRequest, "name is required", "name is required"},
		{"server error drops detail", http.StatusInternalServerError, "pq: refused by 10.1.2.3", ""},
		{"unavailable drops detail", http.StatusServiceUnavailable, "database is down", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			WriteProblem(rr, req, tt.status, tt.detail)

			if got := rr.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Errorf("Content-Type = %q, want application/problem+json", got)
			}
			if rr.Code != tt.status {
				t.Errorf("status = %d, want %d", rr.Code, tt.status)
			}
			var p Problem
			if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
				t.Fatalf("body is not JSON: %v", err)
			}
			if p.Status != tt.status || p.Title != http.StatusText(tt.status) || p.Type != "about:blank" {
				t.Errorf("problem = %+v", p)
			}
			if p.Detail != tt.wantDetail {
				t.Errorf("Detail = %q, want %q", p.Detail, tt.wantDetail)
			}
			if tt.wantDetail == "" && strings.Contains(rr.Body.String(), tt.detail) {
				t.Errorf("body leaks internal text: %s", rr.Body.String())
			}
		})
	}
}

func TestWriteProblemUsesRequestIDHeader(t *testing.T) {
	rr := httptest.NewRecorder()
	rr.Header().Set(RequestIDHeader, "req-42")
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	WriteProblem(rr, req, http.StatusBadRequest, "bad")

	var p Problem
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if p.RequestID != "req-42" {
		t.Errorf("RequestID = %q, want req-42", p.RequestID)
	}
}

func TestWriteError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"decode error keeps status", &DecodeError{Status: http.StatusUnsupportedMediaType, Detail: "wrong type"}, http.StatusUnsupportedMediaType},
		{"unknown error becomes 500", errors.New("pq: secret internal text"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			WriteError(rr, req, tt.err)

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if strings.Contains(rr.Body.String(), "secret") {
				t.Errorf("body leaks internal text: %s", rr.Body.String())
			}
		})
	}
}
