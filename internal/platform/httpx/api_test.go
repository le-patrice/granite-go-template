package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

type echoInput struct {
	Name string `query:"name" doc:"Name to echo"`
}

type echoOutput struct {
	Body struct {
		Message string `json:"message"`
	}
}

func TestNewAPI_ConfigAndSecurity(t *testing.T) {
	router := NewRouter()
	api := NewAPI(router, APIConfig{
		Title:         "Test Service",
		Version:       "1.2.3",
		DocsEnabled:   false,
		PublicBaseURL: "https://api.example.com",
	})

	spec := api.OpenAPI()
	if spec.Info.Title != "Test Service" {
		t.Errorf("Title = %q, want Test Service", spec.Info.Title)
	}
	if spec.Info.Version != "1.2.3" {
		t.Errorf("Version = %q, want 1.2.3", spec.Info.Version)
	}
	if len(spec.Servers) != 1 || spec.Servers[0].URL != "https://api.example.com" {
		t.Errorf("Servers = %+v, want https://api.example.com", spec.Servers)
	}

	sec, ok := spec.Components.SecuritySchemes["bearerAuth"]
	if !ok || sec.Type != "http" || sec.Scheme != "bearer" {
		t.Errorf("expected bearerAuth scheme in OpenAPI components")
	}
}

func TestNewAPI_TypedHandlerExecution(t *testing.T) {
	router := NewRouter()
	api := NewAPI(router, APIConfig{
		Title:       "Test",
		Version:     "1.0.0",
		DocsEnabled: true,
	})

	Register(api, Operation{
		OperationID: "echo",
		Method:      http.MethodGet,
		Path:        "/test/echo",
	}, func(ctx context.Context, input *echoInput) (*echoOutput, error) {
		if input.Name == "" {
			return nil, &Problem{
				Type:   "about:blank",
				Title:  "Bad Request",
				Status: http.StatusBadRequest,
				Detail: "name is required",
			}
		}
		out := &echoOutput{}
		out.Body.Message = "hello " + input.Name
		return out, nil
	})

	// 1. Success case
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/test/echo?name=world", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var res struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.Message != "hello world" {
		t.Errorf("message = %q, want hello world", res.Message)
	}

	// Verify NO $schema in response
	if strings.Contains(rec.Body.String(), "$schema") {
		t.Errorf("response body unexpectedly contains $schema: %s", rec.Body.String())
	}

	// 2. Custom problem error case
	reqErr := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/test/echo", nil)
	recErr := httptest.NewRecorder()
	router.ServeHTTP(recErr, reqErr)

	if recErr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", recErr.Code, recErr.Body.String())
	}
	var prob Problem
	if err := json.Unmarshal(recErr.Body.Bytes(), &prob); err != nil {
		t.Fatalf("unmarshal problem: %v", err)
	}
	if prob.Status != http.StatusBadRequest || prob.Detail != "name is required" {
		t.Errorf("problem = %+v, want status 400, detail 'name is required'", prob)
	}
}

func TestHumaNewError_RFC9457Problem(t *testing.T) {
	SetupHumaErrorModel()

	// 1. Client error with details
	err1 := errors.New("field invalid")
	statusErr := huma.NewError(http.StatusUnprocessableEntity, "validation failed", err1)
	prob, ok := statusErr.(*Problem)
	if !ok {
		t.Fatalf("expected *Problem, got %T", statusErr)
	}
	if prob.Status != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", prob.Status)
	}
	if prob.Detail != "validation failed" {
		t.Errorf("detail = %q, want validation failed", prob.Detail)
	}
	if len(prob.Errors) != 1 || prob.Errors[0].Message != "field invalid" {
		t.Errorf("errors = %+v, want 1 detail with message 'field invalid'", prob.Errors)
	}

	// 2. Server error (500) drops detail and errors
	statusErr500 := huma.NewError(http.StatusInternalServerError, "secret internal failure", errors.New("db down"))
	prob500, ok := statusErr500.(*Problem)
	if !ok {
		t.Fatalf("expected *Problem, got %T", statusErr500)
	}
	if prob500.Status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", prob500.Status)
	}
	if prob500.Detail != "" {
		t.Errorf("500 detail = %q, want empty", prob500.Detail)
	}
	if len(prob500.Errors) != 0 {
		t.Errorf("500 errors = %+v, want empty", prob500.Errors)
	}
}

func TestAddDocumentedRoute(t *testing.T) {
	router := NewRouter()
	api := NewAPI(router, APIConfig{
		Title:   "Test",
		Version: "1.0.0",
	})

	AddDocumentedRoute(api, &Operation{
		OperationID: "healthCheck",
		Method:      http.MethodGet,
		Path:        "/healthz",
		Summary:     "Liveness check",
	})

	spec := api.OpenAPI()
	item, ok := spec.Paths["/healthz"]
	if !ok || item.Get == nil || item.Get.OperationID != "healthCheck" {
		t.Fatalf("expected /healthz route documented in spec: %+v", spec.Paths)
	}
}

func TestAdaptMiddleware(t *testing.T) {
	router := NewRouter()
	api := NewAPI(router, APIConfig{Title: "Test", Version: "1.0.0"})

	type testContextKey struct{}
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Auth") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte("unauthorized"))
				return
			}
			ctx := context.WithValue(r.Context(), testContextKey{}, "test-val")
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	Register(api, Operation{
		OperationID: "guarded",
		Method:      http.MethodGet,
		Path:        "/guarded",
		Middlewares: Middlewares{AdaptMiddleware(mw)},
	}, func(ctx context.Context, input *struct{}) (*struct{ Body string }, error) {
		val, _ := ctx.Value(testContextKey{}).(string)
		return &struct{ Body string }{Body: val}, nil
	})

	// 1. Rejected by middleware
	req1 := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/guarded", nil)
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec1.Code)
	}

	// 2. Allowed by middleware, context passed through
	req2 := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/guarded", nil)
	req2.Header.Set("X-Auth", "secret")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "test-val") {
		t.Fatalf("body = %q, want test-val", rec2.Body.String())
	}
}
