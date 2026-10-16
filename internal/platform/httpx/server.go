package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

const (
	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 15 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 60 * time.Second
	defaultMaxHeaderBytes    = 1 << 16
)

// Router is a ServeMux that records which pattern matched each request, so
// logging, tracing, and metrics can label by route instead of raw path.
type Router struct {
	mux      *http.ServeMux
	patterns []string
}

// NewRouter returns an empty Router.
func NewRouter() *Router {
	return &Router{mux: http.NewServeMux()}
}

// Handle registers h for a Go 1.22 pattern such as "GET /v1/orders/{id}".
// Like ServeMux.Handle it panics on an invalid or conflicting pattern, which
// can only happen while wiring at startup.
func (rt *Router) Handle(pattern string, h http.Handler) {
	rt.patterns = append(rt.patterns, pattern)
	rt.mux.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordRoute(r.Context(), pattern)
		h.ServeHTTP(w, r)
	}))
}

// HandleFunc is Handle for a handler function.
func (rt *Router) HandleFunc(pattern string, f func(http.ResponseWriter, *http.Request)) {
	rt.Handle(pattern, http.HandlerFunc(f))
}

// Patterns returns a copy of all patterns registered on the router.
func (rt *Router) Patterns() []string {
	cp := make([]string, len(rt.patterns))
	copy(cp, rt.patterns)
	return cp
}

// ServeHTTP implements http.Handler.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt.mux.ServeHTTP(w, r)
}

// ServerConfig describes an http.Server. Zero timeouts take safe defaults, so
// a server built here always has every timeout and the header cap set.
type ServerConfig struct {
	Addr              string
	Handler           http.Handler
	Logger            *slog.Logger
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
}

// NewServer builds an http.Server whose internal errors go to the slog logger.
func NewServer(cfg ServerConfig) *http.Server {
	if cfg.ReadHeaderTimeout <= 0 {
		cfg.ReadHeaderTimeout = defaultReadHeaderTimeout
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = defaultReadTimeout
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = defaultWriteTimeout
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = defaultIdleTimeout
	}
	if cfg.MaxHeaderBytes <= 0 {
		cfg.MaxHeaderBytes = defaultMaxHeaderBytes
	}
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           cfg.Handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(cfg.Logger.Handler(), slog.LevelError),
	}
}

// Run serves until ctx is cancelled or the listener fails. On cancellation it
// calls Server.Shutdown with shutdownTimeout and, if draining does not finish
// in time, closes the remaining connections. The caller closes other resources
// (such as the database pool) after Run returns.
func Run(ctx context.Context, srv *http.Server, shutdownTimeout time.Duration) error {
	serveErr := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("httpx: serve: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		<-serveErr
		return fmt.Errorf("httpx: shutdown: %w", err)
	}
	if err := <-serveErr; err != nil {
		return fmt.Errorf("httpx: serve: %w", err)
	}
	return nil
}
