package httpx

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func TestNewServerSetsEveryTimeout(t *testing.T) {
	srv := NewServer(ServerConfig{Addr: ":0", Handler: http.NotFoundHandler(), Logger: discardLogger()})

	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.WriteTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Errorf("timeouts not all set: %v %v %v %v",
			srv.ReadHeaderTimeout, srv.ReadTimeout, srv.WriteTimeout, srv.IdleTimeout)
	}
	if srv.MaxHeaderBytes <= 0 {
		t.Errorf("MaxHeaderBytes = %d, want positive", srv.MaxHeaderBytes)
	}
	if srv.ErrorLog == nil {
		t.Error("ErrorLog must route through slog")
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	defer goleak.VerifyNone(t)

	srv := NewServer(ServerConfig{
		Addr:    "127.0.0.1:0",
		Handler: http.NotFoundHandler(),
		Logger:  discardLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, srv, time.Second) }()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil after a clean shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
}

func TestRunReturnsListenError(t *testing.T) {
	defer goleak.VerifyNone(t)

	srv := NewServer(ServerConfig{
		Addr:    "127.0.0.1:-1",
		Handler: http.NotFoundHandler(),
		Logger:  discardLogger(),
	})
	if err := Run(context.Background(), srv, time.Second); err == nil {
		t.Fatal("Run returned nil, want a listen error for an invalid port")
	}
}
