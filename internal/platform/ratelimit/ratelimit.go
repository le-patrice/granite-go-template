// Package ratelimit provides an in-process keyed token-bucket limiter with
// bounded memory, and the HTTP middleware that uses it.
package ratelimit

import (
	"errors"
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"example.com/app/internal/platform/httpx"
)

// Config configures a Limiter.
type Config struct {
	// Rate is the refill rate per key, in tokens per second.
	Rate rate.Limit
	// Burst is the bucket size per key.
	Burst int
	// IdleTTL is how long a key may go unused before it is evicted.
	IdleTTL time.Duration
	// MaxKeys is a hard cap on tracked keys. At the cap, an arbitrary key is
	// evicted to make room, so memory stays bounded under a key flood.
	MaxKeys int
	// Now is the clock. It defaults to time.Now and exists for tests.
	Now func() time.Time
}

type entry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// Limiter keeps one token bucket per key. It starts no goroutines: idle keys
// are swept lazily from Allow.
type Limiter struct {
	cfg       Config
	mu        sync.Mutex
	entries   map[string]*entry
	lastSweep time.Time
}

// New validates cfg and returns a Limiter.
func New(cfg Config) (*Limiter, error) {
	if cfg.Rate <= 0 || cfg.Burst < 1 || cfg.IdleTTL <= 0 || cfg.MaxKeys < 1 {
		return nil, errors.New("ratelimit: rate, burst, idle TTL, and max keys must all be positive")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Limiter{
		cfg:       cfg,
		entries:   make(map[string]*entry),
		lastSweep: cfg.Now(),
	}, nil
}

// PerMinute converts a per-minute allowance into a rate.
func PerMinute(n int) rate.Limit {
	return rate.Limit(float64(n) / 60)
}

// Allow takes one token for key. When the bucket is empty it returns false and
// how long until a token is available.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.cfg.Now()
	if now.Sub(l.lastSweep) >= l.cfg.IdleTTL {
		l.sweep(now)
	}

	e, ok := l.entries[key]
	if !ok {
		if len(l.entries) >= l.cfg.MaxKeys {
			l.evictOne()
		}
		e = &entry{limiter: rate.NewLimiter(l.cfg.Rate, l.cfg.Burst)}
		l.entries[key] = e
	}
	e.lastSeen = now

	reservation := e.limiter.ReserveN(now, 1)
	if !reservation.OK() {
		return false, time.Second
	}
	if delay := reservation.DelayFrom(now); delay > 0 {
		reservation.CancelAt(now)
		return false, delay
	}
	return true, 0
}

// Len returns the number of tracked keys.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

func (l *Limiter) sweep(now time.Time) {
	for key, e := range l.entries {
		if now.Sub(e.lastSeen) > l.cfg.IdleTTL {
			delete(l.entries, key)
		}
	}
	l.lastSweep = now
}

func (l *Limiter) evictOne() {
	for key := range l.entries {
		delete(l.entries, key)
		return
	}
}

// ByClientIP keys requests by the address httpx.RealIP resolved, falling back
// to the socket address without its port.
func ByClientIP(r *http.Request) string {
	if ip := httpx.ClientIPFrom(r.Context()); ip != "" {
		return ip
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// Middleware answers 429 with a Retry-After header when the request's key has
// no tokens left.
func Middleware(l *Limiter, key func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			allowed, retryAfter := l.Allow(key(r))
			if !allowed {
				seconds := int(math.Ceil(retryAfter.Seconds()))
				if seconds < 1 {
					seconds = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(seconds))
				httpx.WriteProblem(w, r, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
