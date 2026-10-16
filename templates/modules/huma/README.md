# Module: huma

A typed HTTP operation adapter and code-first OpenAPI 3.1 generator built on [Huma v2](https://github.com/danielgtaylor/huma/v2) and Go 1.22 `http.ServeMux`.

## When to adopt

- **Already included** when the `huma` add-on is applied to `granite-go`.
- Adopt when your team wants typed HTTP request/response validation, automatic OpenAPI 3.1 generation, unified RFC 9457 error responses, and optional built-in interactive API docs without runtime reflection overhead in domain logic.

## What it adds

- `github.com/danielgtaylor/huma/v2` dependency integrated cleanly with `platform/httpx`.
- `httpx.NewAPI` and `httpx.Register`: Typed operation registration mounted directly onto `*httpx.Router`.
- Unified RFC 9457 error model: All client errors (validation, auth, domain errors) map to RFC 9457 `*httpx.Problem` with field-level details.
- Suppression of `$schema` response body injection via hook configuration (`config.CreateHooks = nil`).
- `cmd/openapi`: Standalone CLI to export OpenAPI 3.1 and 3.0.3 YAML specifications without running the server.
- `make openapi` and `make openapi-check`: Deterministic generation and CI drift verification.
- Route coverage testing asserting that every router pattern is documented in the OpenAPI specification or explicitly exempted.
- Architectural boundary enforcement: `huma-boundary` depguard rule ensuring feature code never imports Huma directly.

## Usage

Feature handlers define typed input and output structs and register operations through `httpx.Register`:

```go
type createInput struct {
    Body struct {
        Reference string `json:"reference" maxLength:"64"`
        Amount    int64  `json:"amount_minor" minimum:"1"`
    }
}

type createOutput struct {
    Location string `header:"Location"`
    Body     orderResponse
}

httpx.Register(api, httpx.Operation{
    OperationID:   "createOrder",
    Method:        http.MethodPost,
    Path:          "/v1/orders",
    Summary:       "Create a new order",
    Tags:          []string{"Orders"},
    DefaultStatus: http.StatusCreated,
    Security:      []map[string][]string{{"bearerAuth": {}}},
    Middlewares:   httpx.Middlewares{httpx.AdaptMiddleware(requireAuth)},
}, h.create)
```

## Uninstallation / Removal

To remove Huma and return to vanilla `net/http` handlers:

1. Replace Huma operations in each `handler.go` with standard `http.HandlerFunc` using `httpx.Decode`, `httpx.WriteJSON`, and `httpx.WriteProblem`.
2. Remove `cmd/openapi` and `internal/app/spec.go`:
   ```bash
   rm -rf cmd/openapi internal/app/spec.go
   ```
3. Remove `openapi` and `openapi-check` targets from `Makefile` and remove the OpenAPI drift check step from `.github/workflows/ci.yml`.
4. Delete Huma helpers in `internal/platform/httpx/api.go` and remove `huma-boundary` rule from `.golangci.yml`.
5. Remove `github.com/danielgtaylor/huma/v2` dependency:
   ```bash
   go get github.com/danielgtaylor/huma/v2@none
   go mod tidy
   ```
6. Keep the last generated `api/openapi.yaml` as the starting point for a hand-maintained spec. Domain entities and service use cases require zero modifications.
