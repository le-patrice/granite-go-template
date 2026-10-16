package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type payload struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func newBodyRequest(body, contentType string) (*httptest.ResponseRecorder, *http.Request) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	return httptest.NewRecorder(), r
}

func TestDecode(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		contentType string
		wantStatus  int
	}{
		{"valid", `{"name":"a","count":1}`, "application/json", 0},
		{"valid with charset", `{"name":"a","count":1}`, "application/json; charset=utf-8", 0},
		{"valid with trailing whitespace", "{\"name\":\"a\"}\n\n", "application/json", 0},
		{"wrong content type", `{"name":"a"}`, "text/plain", http.StatusUnsupportedMediaType},
		{"missing content type", `{"name":"a"}`, "", http.StatusUnsupportedMediaType},
		{"empty body", "", "application/json", http.StatusBadRequest},
		{"truncated json", `{"name":`, "application/json", http.StatusBadRequest},
		{"malformed json", `{name: 1}`, "application/json", http.StatusBadRequest},
		{"unknown field", `{"name":"a","extra":1}`, "application/json", http.StatusBadRequest},
		{"wrong field type", `{"name":1}`, "application/json", http.StatusBadRequest},
		{"second json value", `{"name":"a"} {"name":"b"}`, "application/json", http.StatusBadRequest},
		{"trailing garbage", `{"name":"a"}x`, "application/json", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, r := newBodyRequest(tt.body, tt.contentType)
			var got payload
			err := Decode(w, r, &got)

			if tt.wantStatus == 0 {
				if err != nil {
					t.Fatalf("Decode returned %v, want nil", err)
				}
				if got.Name != "a" {
					t.Errorf("Name = %q, want a", got.Name)
				}
				return
			}

			var decodeErr *DecodeError
			if !errors.As(err, &decodeErr) {
				t.Fatalf("Decode returned %v, want *DecodeError", err)
			}
			if decodeErr.Status != tt.wantStatus {
				t.Errorf("Status = %d, want %d (detail %q)", decodeErr.Status, tt.wantStatus, decodeErr.Detail)
			}
		})
	}
}

func TestDecodeLimit(t *testing.T) {
	w, r := newBodyRequest(`{"name":"`+strings.Repeat("x", 64)+`"}`, "application/json")
	var got payload
	err := DecodeLimit(w, r, &got, 16)

	var decodeErr *DecodeError
	if !errors.As(err, &decodeErr) {
		t.Fatalf("DecodeLimit returned %v, want *DecodeError", err)
	}
	if decodeErr.Status != http.StatusRequestEntityTooLarge {
		t.Errorf("Status = %d, want 413", decodeErr.Status)
	}
}
