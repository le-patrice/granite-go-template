package httpx

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics owns a private Prometheus registry (no global state) with request
// count and latency labelled by route pattern, never by raw path.
type Metrics struct {
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// NewMetrics builds the registry with the HTTP collectors plus Go runtime and
// process collectors.
func NewMetrics() (*Metrics, error) {
	registry := prometheus.NewRegistry()
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "HTTP requests by route pattern and status code.",
	}, []string{"route", "status"})
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency by route pattern.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route"})

	collectorsToRegister := []prometheus.Collector{
		requests,
		duration,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	}
	for _, c := range collectorsToRegister {
		if err := registry.Register(c); err != nil {
			return nil, fmt.Errorf("httpx: register metrics: %w", err)
		}
	}
	return &Metrics{registry: registry, requests: requests, duration: duration}, nil
}

// Registerer lets other packages add their own collectors to the same registry.
func (m *Metrics) Registerer() prometheus.Registerer { return m.registry }

// Handler serves the registry in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// Middleware records one count and one latency observation per request. It
// must run after Telemetry, which installs the route holder it reads.
func (m *Metrics) Middleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			rec := &statusRecorder{ResponseWriter: w}
			start := time.Now()
			defer func() {
				p := recover()
				route := RoutePattern(ctx)
				status := rec.statusCode(p != nil)
				m.requests.WithLabelValues(route, strconv.Itoa(status)).Inc()
				m.duration.WithLabelValues(route).Observe(time.Since(start).Seconds())
				if p != nil {
					panic(p)
				}
			}()
			next.ServeHTTP(rec, r)
		})
	}
}
