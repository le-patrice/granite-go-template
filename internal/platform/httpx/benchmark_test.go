package httpx_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"example.com/app/internal/platform/httpx"
)

type healthResponse struct {
	Status string `json:"status"`
}

type healthOutput struct {
	Body healthResponse
}

func BenchmarkPlainHandler(b *testing.B) {
	router := httpx.NewRouter()
	router.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
	}
}

func BenchmarkHumaOperation(b *testing.B) {
	router := httpx.NewRouter()
	api := httpx.NewAPI(router, httpx.APIConfig{
		Title:       "Bench",
		Version:     "1.0.0",
		DocsEnabled: false,
	})

	httpx.Register(api, httpx.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/health",
	}, func(ctx context.Context, input *struct{}) (*healthOutput, error) {
		return &healthOutput{Body: healthResponse{Status: "ok"}}, nil
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
	}
}
