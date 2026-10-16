package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// RequestIDHeader carries the request ID in both directions.
const RequestIDHeader = "X-Request-Id"

const unmatchedRoute = "unmatched"

type (
	requestIDKey struct{}
	routeKey     struct{}
)

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middleware so that the first one listed is the outermost.
func Chain(h http.Handler, middleware ...Middleware) http.Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		h = middleware[i](h)
	}
	return h
}

// statusRecorder captures the status code and byte count of a response.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		s.ResponseWriter.WriteHeader(code)
		return
	}
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wrote {
		s.WriteHeader(http.StatusOK)
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusRecorder) statusCode(panicked bool) int {
	if s.wrote {
		return s.status
	}
	if panicked {
		return http.StatusInternalServerError
	}
	return http.StatusOK
}

// routeHolder lets the Router report the matched pattern to middleware that
// runs outside the mux. It works on Go 1.22, where Request.Pattern is absent.
type routeHolder struct {
	pattern string
}

func recordRoute(ctx context.Context, pattern string) {
	if holder, ok := ctx.Value(routeKey{}).(*routeHolder); ok {
		holder.pattern = pattern
	}
}

// RoutePattern returns the matched route pattern, or "unmatched" when no
// registered route handled the request. It is safe to use as a metric label.
func RoutePattern(ctx context.Context) string {
	if holder, ok := ctx.Value(routeKey{}).(*routeHolder); ok && holder.pattern != "" {
		return holder.pattern
	}
	return unmatchedRoute
}

// Recover turns a panic into a generic 500 problem and logs the stack with the
// request ID. It reads the ID from the response header that RequestID sets, so
// it can sit outside RequestID in the chain. http.ErrAbortHandler is re-raised.
func Recover(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			rec := &statusRecorder{ResponseWriter: w}
			defer func() {
				p := recover()
				if p == nil {
					return
				}
				if err, ok := p.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(p)
				}
				logger.ErrorContext(ctx, "panic recovered",
					slog.String("request_id", w.Header().Get(RequestIDHeader)),
					slog.String("panic", fmt.Sprint(p)),
					slog.String("stack", string(debug.Stack())),
				)
				if !rec.wrote {
					WriteInternal(rec, r)
				}
			}()
			next.ServeHTTP(rec, r)
		})
	}
}

// RequestID keeps a well-formed inbound X-Request-Id or generates one, echoes
// it on the response, and stores it in the context.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(RequestIDHeader)
			if !validRequestID(id) {
				id = newRequestID()
			}
			w.Header().Set(RequestIDHeader, id)
			ctx := context.WithValue(r.Context(), requestIDKey{}, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequestIDFrom returns the request ID stored by RequestID, or "".
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

// Telemetry starts a server span (otelhttp) and writes one access-log record
// per request. It also installs the route holder that Router fills in, so the
// span name and the log carry the route pattern, never the raw path.
func Telemetry(logger *slog.Logger, tp trace.TracerProvider, prop propagation.TextMapPropagator) Middleware {
	return func(next http.Handler) http.Handler {
		observed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			holder := &routeHolder{}
			ctx := context.WithValue(r.Context(), routeKey{}, holder)
			r = r.WithContext(ctx)

			rec := &statusRecorder{ResponseWriter: w}
			start := time.Now()
			defer func() {
				p := recover()
				route := RoutePattern(ctx)
				status := rec.statusCode(p != nil)

				span := trace.SpanFromContext(ctx)
				span.SetName(r.Method + " " + route)
				span.SetAttributes(attribute.String("http.route", route))

				attrs := []slog.Attr{
					slog.String("method", r.Method),
					slog.String("route", route),
					slog.Int("status", status),
					slog.Int64("bytes", rec.bytes),
					slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
					slog.String("request_id", RequestIDFrom(ctx)),
					slog.String("client_ip", ClientIPFrom(ctx)),
				}
				if sc := span.SpanContext(); sc.IsValid() {
					attrs = append(attrs, slog.String("trace_id", sc.TraceID().String()))
				}
				level := slog.LevelInfo
				if status >= http.StatusInternalServerError {
					level = slog.LevelError
				}
				logger.LogAttrs(ctx, level, "http request", attrs...)

				if p != nil {
					panic(p)
				}
			}()
			next.ServeHTTP(rec, r)
		})

		return otelhttp.NewHandler(observed, "http.server",
			otelhttp.WithTracerProvider(tp),
			otelhttp.WithPropagators(prop),
			otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
				return "HTTP " + r.Method
			}),
		)
	}
}
