# Verified Symbols: Huma v2 (`github.com/danielgtaylor/huma/v2`)

Date: 2026-10-16
Version: `v2.39.1`
Status: Confirmed against `go doc`

## 1. Core Adapter & Routing
- `humago.New(mux *http.ServeMux, config huma.Config) huma.API`: Mounts Huma onto the standard library `http.ServeMux`. Works with Go 1.22+ ServeMux.
- `huma.Register[I, O any](api huma.API, op huma.Operation, handler func(context.Context, *I) (*O, error))`: Registers a strongly typed operation.
- `huma.Operation`:
  - `OperationID string`: Unique operation identifier.
  - `Method string`: HTTP method (e.g., `http.MethodGet`, `http.MethodPost`).
  - `Path string`: URL pattern (e.g., `/v1/orders/{id}`).
  - `Summary string`, `Description string`, `Tags []string`: Documentation metadata.
  - `Security []map[string][]string`: Security requirements (e.g., `{"bearerAuth": {}}`).
  - `Responses map[string]*huma.Response`: Possible HTTP responses.
  - `Middlewares huma.Middlewares`: Operation-specific middleware chain.
  - `MaxBodyBytes int64`: Body size limit (default 1MB; returns 413 if exceeded).
  - `DefaultStatus int`: Default status code (200, 201, 204).

## 2. OpenAPI Spec & Raw Routes
- `api.OpenAPI() *huma.OpenAPI`: Returns the live OpenAPI specification model.
- `api.OpenAPI().AddOperation(op *huma.Operation)`: Adds an operation directly to the OpenAPI specification without registering a handler on the underlying router. Used for documenting raw routes (`/healthz`, `/readyz`, `/metrics`, `POST /v1/webhooks/{source}`).
- `api.OpenAPI().YAML() ([]byte, error)`: Exports the OpenAPI 3.1 specification as YAML bytes.
- `api.OpenAPI().Downgrade() ([]byte, error)`: Converts OpenAPI 3.1 to OpenAPI 3.0.3 JSON for compatibility with legacy tooling.

## 3. Schema Transformers & $schema Suppression
- In `huma.DefaultConfig(title, version)`: By default, `CreateHooks` registers a `NewSchemaLinkTransformer` that injects `$schema` fields and `Link` headers into response bodies.
- Setting `config.CreateHooks = nil` completely disables this transformer, preventing `$schema` fields from appearing in JSON responses while retaining standard JSON Schema components in `/openapi.yaml`.

## 4. Error Model & RFC 9457 Integration
- `huma.StatusError` interface:
  ```go
  type StatusError interface {
      GetStatus() int
      Error() string
  }
  ```
- `huma.NewError`: Global error constructor `var NewError = func(status int, msg string, errs ...error) StatusError`. Overriding this function allows returning `*httpx.Problem`, unifying error responses across Huma and standard HTTP handlers.
- `huma.ErrorDetailer`: `ErrorDetail() *huma.ErrorDetail` allows extracting field validation errors (`message`, `location`, `value`) to populate the `errors` array in 422 Problem Details.

## 5. Security Scheme
- OpenTelemetry and auth schemes can be attached to `api.OpenAPI().Components.SecuritySchemes["bearerAuth"] = &huma.SecurityScheme{ Type: "http", Scheme: "bearer", BearerFormat: "PASETO" }`.

## 6. Testing Tooling
- `humatest.New(t, config)`: Creates an in-memory test API and router.
