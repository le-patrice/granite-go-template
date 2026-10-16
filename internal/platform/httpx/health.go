package httpx

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// Check reports whether one dependency is ready. It must honor ctx.
type Check func(ctx context.Context) error

type statusBody struct {
	Status string `json:"status"`
}

func writeStatus(w http.ResponseWriter, code int, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(statusBody{Status: status})
}

// Healthz is liveness: it answers 200 whenever the process is serving.
func Healthz() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusOK, "ok")
	})
}

// Readyz is readiness: it runs every check under one shared timeout and answers
// 503 if any fails. The failure reason is logged, never returned to the client.
func Readyz(logger *slog.Logger, timeout time.Duration, checks ...Check) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		for _, check := range checks {
			if err := check(ctx); err != nil {
				logger.WarnContext(r.Context(), "readiness check failed", "error", err)
				WriteProblem(w, r, http.StatusServiceUnavailable, "")
				return
			}
		}
		writeStatus(w, http.StatusOK, "ok")
	})
}
