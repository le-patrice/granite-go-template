package acme

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"example.com/app/internal/platform/webhook"
)

const secret = "local-only-webhook-secret"

func TestVerifier(t *testing.T) {
	v, err := NewVerifier(secret)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	body := []byte(`{"id":"evt_1","type":"ping"}`)
	good := Sign(secret, body)

	tests := []struct {
		name   string
		body   []byte
		header string
		valid  bool
	}{
		{"valid signature", body, good, true},
		{"tampered body", []byte(`{"id":"evt_2","type":"ping"}`), good, false},
		{"missing header", body, "", false},
		{"wrong prefix", body, strings.Replace(good, "sha256=", "sha1=", 1), false},
		{"not hex", body, "sha256=zzzz", false},
		{"signed with another secret", body, Sign("another-secret-value", body), false},
		{"truncated signature", body, good[:len(good)-2], false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := http.Header{}
			if tt.header != "" {
				header.Set(SignatureHeader, tt.header)
			}
			err := v.Verify(tt.body, header)
			if tt.valid && err != nil {
				t.Errorf("Verify = %v, want nil", err)
			}
			if !tt.valid && !errors.Is(err, webhook.ErrInvalidSignature) {
				t.Errorf("Verify = %v, want ErrInvalidSignature", err)
			}
		})
	}
}

func TestNewVerifierRejectsShortSecrets(t *testing.T) {
	if _, err := NewVerifier("short"); err == nil {
		t.Error("expected an error for a short secret")
	}
	if _, err := WebhookSource(""); err == nil {
		t.Error("expected an error for an empty secret")
	}
}

func TestParseEvent(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"valid", `{"id":"evt_1","type":"ping","data":{"a":1}}`, false},
		{"not json", `nope`, true},
		{"missing id", `{"type":"ping"}`, true},
		{"missing type", `{"id":"evt_1"}`, true},
		{"overlong id", `{"id":"` + strings.Repeat("x", 256) + `","type":"ping"}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseEvent([]byte(tt.body))
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, ErrInvalidEvent) {
				t.Errorf("err = %v, want ErrInvalidEvent", err)
			}
		})
	}
	if id, err := ExternalID([]byte(`{"id":"evt_7","type":"ping"}`)); err != nil || id != "evt_7" {
		t.Errorf("ExternalID = %q, %v", id, err)
	}
}

func newTestClient(t *testing.T, server *httptest.Server, retries int) *Client {
	t.Helper()
	client, err := NewClient(ClientConfig{BaseURL: server.URL, APIKey: "key-123", MaxRetries: retries})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func TestGetEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/events/evt%2F1" && r.URL.Path != "/v1/events/evt/1" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer key-123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(Event{ID: "evt/1", Type: "ping"})
	}))
	defer server.Close()
	client := newTestClient(t, server, 0)

	event, err := client.GetEvent(context.Background(), "evt/1")
	if err != nil || event.Type != "ping" {
		t.Fatalf("GetEvent = %+v, %v", event, err)
	}

	_, err = client.GetEvent(context.Background(), "missing")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		t.Errorf("err = %v, want an APIError with status 404", err)
	}
}

func TestNotifyIsRetriedWithItsIdempotencyKey(t *testing.T) {
	var calls atomic.Int32
	var keys, bodies [2]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n <= 2 {
			keys[n-1] = r.Header.Get("Idempotency-Key")
			raw, _ := io.ReadAll(r.Body)
			bodies[n-1] = string(raw)
		}
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	client := newTestClient(t, server, 2)

	if err := client.Notify(context.Background(), "order-42", map[string]string{"order": "42"}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("server saw %d calls, want 2 (one retry)", got)
	}
	if keys[0] != "order-42" || keys[1] != "order-42" {
		t.Errorf("idempotency keys = %v, want the same key on both attempts", keys)
	}
	if bodies[0] != bodies[1] || bodies[0] == "" {
		t.Errorf("bodies = %q, want the same body replayed", bodies)
	}
}

func TestNewClientValidatesItsConfig(t *testing.T) {
	for name, cfg := range map[string]ClientConfig{
		"no scheme":  {BaseURL: "acme.example", APIKey: "k"},
		"no host":    {BaseURL: "https://", APIKey: "k"},
		"no api key": {BaseURL: "https://acme.example"},
	} {
		if _, err := NewClient(cfg); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
