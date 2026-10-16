// Package telemetry sets up structured logging and OpenTelemetry tracing.
// It takes plain values, never the config package.
package telemetry

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const redacted = "[redacted]"

// NewLogger returns a JSON slog logger that tags every record with the service
// name and environment, and redacts attributes whose key names a secret.
func NewLogger(w io.Writer, level, service, environment string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("telemetry: log level %q: %w", level, err)
	}
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: lvl,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if isSensitiveKey(a.Key) {
				return slog.String(a.Key, redacted)
			}
			return a
		},
	})
	return slog.New(handler).With(
		slog.String("service", service),
		slog.String("env", environment),
	), nil
}

func isSensitiveKey(key string) bool {
	switch strings.ToLower(key) {
	case "password", "passwd", "password_hash", "token", "access_token",
		"refresh_token", "authorization", "secret", "api_key":
		return true
	default:
		return false
	}
}

// Options configures Setup.
type Options struct {
	ServiceName string
	// Endpoint is the OTLP/HTTP collector URL. Empty means tracing is a no-op.
	Endpoint string
}

// Provider carries the tracing pieces the HTTP layer needs. No OpenTelemetry
// globals are set: callers pass these values explicitly.
type Provider struct {
	TracerProvider trace.TracerProvider
	Propagator     propagation.TextMapPropagator
	shutdown       func(context.Context) error
}

// Shutdown flushes and stops the exporter. It is safe to call on a no-op Provider.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p.shutdown == nil {
		return nil
	}
	return p.shutdown(ctx)
}

// Setup builds the tracing provider. When Options.Endpoint is empty it returns
// a no-op provider that still propagates incoming trace context.
func Setup(ctx context.Context, opts Options) (*Provider, error) {
	propagator := propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
	if opts.Endpoint == "" {
		return &Provider{
			TracerProvider: noop.NewTracerProvider(),
			Propagator:     propagator,
		}, nil
	}

	res, err := resource.New(ctx, resource.WithAttributes(
		attribute.String("service.name", opts.ServiceName),
	))
	if err != nil {
		return nil, fmt.Errorf("telemetry: resource: %w", err)
	}
	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(opts.Endpoint))
	if err != nil {
		return nil, fmt.Errorf("telemetry: otlp exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	return &Provider{
		TracerProvider: tp,
		Propagator:     propagator,
		shutdown:       tp.Shutdown,
	}, nil
}
