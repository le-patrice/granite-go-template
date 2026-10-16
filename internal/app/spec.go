package app

import (
	"io"
	"log/slog"

	"github.com/danielgtaylor/huma/v2"

	"example.com/app/internal/config"
	"example.com/app/internal/orders"
	"example.com/app/internal/platform/httpx"
	"example.com/app/internal/users"
)

// BuildOpenAPI constructs a router and API instance, registers all routes and
// documented operational endpoints, and returns the complete OpenAPI specification.
func BuildOpenAPI(cfg config.Config) (*huma.OpenAPI, error) {
	router := httpx.NewRouter()
	api := httpx.NewAPI(router, httpx.APIConfig{
		Title:         cfg.ServiceName,
		Version:       cfg.AppVersion,
		DocsEnabled:   cfg.DocsEnabled,
		PublicBaseURL: cfg.PublicBaseURL,
	})

	RegisterDocumentedRoutes(api)

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	users.NewHandler(nil, logger).Routes(api, nil, nil)
	orders.NewHandler(nil, logger).Routes(api, nil, nil)

	return api.OpenAPI(), nil
}
