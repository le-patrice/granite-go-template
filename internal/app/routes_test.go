package app

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"example.com/app/internal/orders"
	"example.com/app/internal/platform/auth"
	"example.com/app/internal/platform/httpx"
	"example.com/app/internal/users"
)

// checkRouteCoverage checks that every pattern registered on router appears
// in spec.Paths, unless exempted with an explicit reason.
func checkRouteCoverage(patterns []string, spec *huma.OpenAPI, exemptions map[string]string) []string {
	var missing []string

	for _, pattern := range patterns {
		if reason, ok := exemptions[pattern]; ok && reason != "" {
			continue
		}

		method, path, ok := strings.Cut(pattern, " ")
		if !ok {
			// Path without method prefix (matches all methods or GET)
			method = http.MethodGet
			path = pattern
		}

		pathItem, exists := spec.Paths[path]
		if !exists {
			missing = append(missing, fmt.Sprintf("path %q not in spec", path))
			continue
		}

		var hasOp bool
		switch strings.ToUpper(method) {
		case http.MethodGet:
			hasOp = pathItem.Get != nil
		case http.MethodPost:
			hasOp = pathItem.Post != nil
		case http.MethodPut:
			hasOp = pathItem.Put != nil
		case http.MethodDelete:
			hasOp = pathItem.Delete != nil
		case http.MethodPatch:
			hasOp = pathItem.Patch != nil
		case http.MethodHead:
			hasOp = pathItem.Head != nil
		case http.MethodOptions:
			hasOp = pathItem.Options != nil
		}

		if !hasOp {
			missing = append(missing, fmt.Sprintf("%s %s missing operation in spec", method, path))
		}
	}

	return missing
}

func setupTestRouterAndAPI(t *testing.T) (*httpx.Router, huma.API) {
	t.Helper()
	clock := func() time.Time { return time.Now().UTC() }
	hasher, err := auth.NewHasher(auth.PasswordParams{MemoryKiB: 64, Time: 1, Threads: 1})
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}

	tm, err := auth.NewTokenManager(auth.TokenConfig{
		Key:       bytes.Repeat([]byte{7}, 32),
		AccessTTL: 15 * time.Minute,
		Now:       clock,
	})
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}

	userRepo := newCharUserRepo()
	userService := users.NewService(userRepo, hasher, tm, users.Options{
		RefreshTTL: 24 * time.Hour,
		Now:        clock,
	})

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	userHandler := users.NewHandler(userService, logger)

	orderRepo := &charOrderRepo{}
	orderService := orders.NewService(orderRepo)
	orderHandler := orders.NewHandler(orderService, logger)

	router := httpx.NewRouter()
	router.Handle("GET /healthz", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	router.Handle("GET /readyz", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	router.Handle("GET /metrics", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	router.Handle("POST /v1/webhooks/{source}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	api := httpx.NewAPI(router, httpx.APIConfig{
		Title:       "Test Service",
		Version:     "1.0.0",
		DocsEnabled: true,
	})
	RegisterDocumentedRoutes(api)

	noopMW := func(h http.Handler) http.Handler { return h }
	authMW := auth.RequireAuth(tm)

	userHandler.Routes(api, noopMW, authMW)
	orderHandler.Routes(api, authMW, nil)

	return router, api
}

func TestRouteCoverage(t *testing.T) {
	router, api := setupTestRouterAndAPI(t)

	// Explicit list of router patterns exempted from OpenAPI documentation, with reasons.
	exemptions := map[string]string{
		"GET /openapi":           "Huma built-in OpenAPI spec endpoint",
		"GET /openapi.json":      "Huma built-in OpenAPI JSON spec endpoint",
		"GET /openapi.yaml":      "Huma built-in OpenAPI YAML spec endpoint",
		"GET /openapi-3.0.json":  "Huma built-in OpenAPI 3.0 JSON spec endpoint",
		"GET /openapi-3.0.yaml":  "Huma built-in OpenAPI 3.0 YAML spec endpoint",
		"GET /docs":              "Huma built-in interactive documentation UI",
		"GET /schemas":           "Huma built-in JSON Schemas endpoint",
		"GET /schemas/{schema}":  "Huma built-in schema detail endpoint",
	}

	patterns := router.Patterns()
	if len(patterns) == 0 {
		t.Fatal("expected registered route patterns on router, got none")
	}

	missing := checkRouteCoverage(patterns, api.OpenAPI(), exemptions)
	if len(missing) > 0 {
		t.Errorf("route coverage check failed: %v", missing)
	}
}

func TestRouteCoverage_UndocumentedRouteFails(t *testing.T) {
	router, api := setupTestRouterAndAPI(t)

	// Intentionally add an undocumented route to the router
	router.Handle("POST /v1/undocumented/secret", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	exemptions := map[string]string{
		"GET /openapi":          "Huma built-in OpenAPI spec endpoint",
		"GET /openapi.json":     "Huma built-in OpenAPI JSON spec endpoint",
		"GET /openapi.yaml":     "Huma built-in OpenAPI YAML spec endpoint",
		"GET /docs":             "Huma built-in interactive documentation UI",
		"GET /schemas":          "Huma built-in JSON Schemas endpoint",
		"GET /schemas/{schema}": "Huma built-in schema detail endpoint",
	}

	missing := checkRouteCoverage(router.Patterns(), api.OpenAPI(), exemptions)
	if len(missing) == 0 {
		t.Fatal("expected checkRouteCoverage to detect undocumented route POST /v1/undocumented/secret, but it reported none")
	}

	found := false
	for _, m := range missing {
		if strings.Contains(m, "/v1/undocumented/secret") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected error mentioning /v1/undocumented/secret, got: %v", missing)
	}
}
