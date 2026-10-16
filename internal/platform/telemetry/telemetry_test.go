package telemetry

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestNewLoggerRejectsBadLevel(t *testing.T) {
	if _, err := NewLogger(&bytes.Buffer{}, "loud", "svc", "local"); err == nil {
		t.Fatal("expected an error for an invalid level")
	}
}

func TestNewLoggerRedactsSecrets(t *testing.T) {
	var buf bytes.Buffer
	logger, err := NewLogger(&buf, "info", "svc", "local")
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	logger.Info("login", "user", "bob", "password", "hunter2", "Token", "abc123")

	out := buf.String()
	for _, leaked := range []string{"hunter2", "abc123"} {
		if strings.Contains(out, leaked) {
			t.Errorf("log output leaks %q: %s", leaked, out)
		}
	}
	if !strings.Contains(out, "bob") || !strings.Contains(out, redacted) {
		t.Errorf("log output missing expected fields: %s", out)
	}
	if !strings.Contains(out, `"service":"svc"`) {
		t.Errorf("log output missing service tag: %s", out)
	}
}

func TestSetupWithoutEndpointIsNoop(t *testing.T) {
	ctx := context.Background()
	p, err := Setup(ctx, Options{ServiceName: "svc"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if p.TracerProvider == nil || p.Propagator == nil {
		t.Fatal("no-op provider must still carry a tracer provider and propagator")
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}
