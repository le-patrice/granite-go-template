package httpx

import (
	"context"
	"crypto/tls"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"reflect"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// Re-exported Huma types so feature packages never import Huma directly.
type (
	// API is the Huma API interface.
	API = huma.API

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

	// Param is an operation parameter.
	Param = huma.Param

	// MediaType is an OpenAPI media type object.
	MediaType = huma.MediaType

	// Tag is an OpenAPI tag object.
	Tag = huma.Tag

	// Header is an OpenAPI header object.
	Header = huma.Header
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

	config.Tags = []*huma.Tag{
		{Name: "Auth", Description: "User registration, authentication sessions, and token management."},
		{Name: "Users", Description: "User profile and account operations."},
		{Name: "Orders", Description: "Order creation, retrieval, and keyset-paginated listing."},
		{Name: "Operations", Description: "Health checks, readiness probes, and telemetry metrics."},
		{Name: "Webhooks", Description: "Inbound webhook ingestion and processing."},
	}

	if config.Components.Responses == nil {
		config.Components.Responses = map[string]*huma.Response{}
	}
	probSchema := huma.SchemaFromType(config.Components.Schemas, reflect.TypeOf(&Problem{}))
	config.Components.Responses["401Unauthorized"] = &huma.Response{
		Description: "Authentication required or access token invalid",
		Headers: map[string]*huma.Header{
			"WWW-Authenticate": {
				Description: "Bearer scheme authentication challenge",
				Schema:      &huma.Schema{Type: "string"},
			},
		},
		Content: map[string]*huma.MediaType{
			"application/problem+json": {
				Schema: probSchema,
			},
		},
	}
	config.Components.Responses["429TooManyRequests"] = &huma.Response{
		Description: "Too many requests; rate limit exceeded",
		Headers: map[string]*huma.Header{
			"Retry-After": {
				Description: "Number of seconds to wait before retrying",
				Schema:      &huma.Schema{Type: "integer"},
			},
		},
		Content: map[string]*huma.MediaType{
			"application/problem+json": {
				Schema: probSchema,
			},
		},
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

// wrappedContext wraps a huma.Context to redirect writes to an adapted http.ResponseWriter.
type wrappedContext struct {
	orig huma.Context
	w    http.ResponseWriter
	r    *http.Request
}

func (c *wrappedContext) Unwrap() huma.Context {
	return c.orig
}

func (c *wrappedContext) Operation() *huma.Operation {
	return c.orig.Operation()
}

func (c *wrappedContext) Context() context.Context {
	return c.r.Context()
}

func (c *wrappedContext) TLS() *tls.ConnectionState {
	return c.orig.TLS()
}

func (c *wrappedContext) Version() huma.ProtoVersion {
	return c.orig.Version()
}

func (c *wrappedContext) Method() string {
	return c.r.Method
}

func (c *wrappedContext) Host() string {
	return c.r.Host
}

func (c *wrappedContext) RemoteAddr() string {
	return c.r.RemoteAddr
}

func (c *wrappedContext) URL() url.URL {
	return *c.r.URL
}

func (c *wrappedContext) Param(name string) string {
	return c.orig.Param(name)
}

func (c *wrappedContext) Query(name string) string {
	return c.orig.Query(name)
}

func (c *wrappedContext) Header(name string) string {
	return c.r.Header.Get(name)
}

func (c *wrappedContext) EachHeader(cb func(name, value string)) {
	c.orig.EachHeader(cb)
}

func (c *wrappedContext) BodyReader() io.Reader {
	return c.r.Body
}

func (c *wrappedContext) GetMultipartForm() (*multipart.Form, error) {
	return c.orig.GetMultipartForm()
}

func (c *wrappedContext) SetReadDeadline(d time.Time) error {
	return c.orig.SetReadDeadline(d)
}

func (c *wrappedContext) SetStatus(code int) {
	c.orig.SetStatus(code)
	c.w.WriteHeader(code)
}

func (c *wrappedContext) Status() int {
	return c.orig.Status()
}

func (c *wrappedContext) SetHeader(name, value string) {
	c.orig.SetHeader(name, value)
	c.w.Header().Set(name, value)
}

func (c *wrappedContext) AppendHeader(name, value string) {
	c.orig.AppendHeader(name, value)
	c.w.Header().Add(name, value)
}

func (c *wrappedContext) BodyWriter() io.Writer {
	return c.w
}

// AdaptMiddleware adapts a standard func(http.Handler) http.Handler middleware
// into a Huma operation middleware.
func AdaptMiddleware(mw func(http.Handler) http.Handler) func(Context, func(Context)) {
	if mw == nil {
		return func(ctx Context, next func(Context)) {
			next(ctx)
		}
	}
	return func(ctx Context, next func(Context)) {
		r, w := humago.Unwrap(ctx)
		handler := mw(http.HandlerFunc(func(w2 http.ResponseWriter, r2 *http.Request) {
			wc := &wrappedContext{
				orig: ctx,
				w:    w2,
				r:    r2,
			}
			next(huma.WithContext(wc, r2.Context()))
		}))
		handler.ServeHTTP(w, r)
	}
}
