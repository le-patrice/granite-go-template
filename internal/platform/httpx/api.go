package httpx

import (
	"context"
	"net/http"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// Re-exported Huma types so feature packages never import Huma directly.
type (
	// Operation describes an API operation.
	Operation = huma.Operation

	// Response describes an operation response.
	Response = huma.Response

	// Schema represents an OpenAPI JSON Schema object.
	Schema = huma.Schema

	// Middlewares is a slice of operation middleware handlers.
	Middlewares = huma.Middlewares

	// Context is the Huma request context.
	Context = huma.Context
)

// APIConfig configures Huma v2 OpenAPI and documentation endpoints.
type APIConfig struct {
	Title         string
	Version       string
	DocsEnabled   bool
	PublicBaseURL string
}

var setupOnce sync.Once

// SetupHumaErrorModel configures Huma to produce RFC 9457 *Problem errors.
func SetupHumaErrorModel() {
	setupOnce.Do(func() {
		huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
			var details []*ErrorDetail
			for _, err := range errs {
				if err == nil {
					continue
				}
				if ed, ok := err.(huma.ErrorDetailer); ok {
					d := ed.ErrorDetail()
					details = append(details, &ErrorDetail{
						Message:  d.Message,
						Location: d.Location,
						Value:    d.Value,
					})
				} else {
					details = append(details, &ErrorDetail{
						Message: err.Error(),
					})
				}
			}

			p := &Problem{
				Type:   "about:blank",
				Title:  http.StatusText(status),
				Status: status,
				Detail: msg,
				Errors: details,
			}
			if status >= http.StatusInternalServerError {
				p.Detail = ""
				p.Errors = nil
			}
			return p
		}
	})
}

// NewAPI constructs a new Huma v2 API mounted on router.
func NewAPI(r *Router, cfg APIConfig) huma.API {
	SetupHumaErrorModel()

	config := huma.DefaultConfig(cfg.Title, cfg.Version)
	config.OpenAPIPath = "/openapi"
	if !cfg.DocsEnabled {
		config.DocsPath = ""
		config.DocsRenderer = ""
	}

	// Remove default link transformer so responses do not include $schema fields
	config.CreateHooks = nil

	if cfg.PublicBaseURL != "" {
		config.Servers = []*huma.Server{
			{URL: cfg.PublicBaseURL},
		}
	}

	if config.Components == nil {
		config.Components = &huma.Components{}
	}
	if config.Components.SecuritySchemes == nil {
		config.Components.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	config.Components.SecuritySchemes["bearerAuth"] = &huma.SecurityScheme{
		Type:         "http",
		Scheme:       "bearer",
		BearerFormat: "PASETO",
		Description:  "PASETO v4.local bearer access token",
	}

	return humago.New(r, config)
}

// Register registers a typed operation handler on api.
func Register[I, O any](api huma.API, op Operation, handler func(context.Context, *I) (*O, error)) {
	huma.Register(api, op, handler)
}

// AddDocumentedRoute documents a raw route directly in the OpenAPI spec without handling it through Huma.
func AddDocumentedRoute(api huma.API, op *Operation) {
	api.OpenAPI().AddOperation(op)
}
