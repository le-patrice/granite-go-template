// Package config loads and validates process configuration from the environment.
package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// Allowed values for Config.Env.
const (
	EnvLocal      = "local"
	EnvStaging    = "staging"
	EnvProduction = "production"
)

const tokenKeyBytes = 32

// Config holds every per-project setting. Values come from environment
// variables only. Secrets are never logged: use LogValue when logging a Config.
type Config struct {
	Env         string `env:"ENV" envDefault:"local"`
	ServiceName string `env:"SERVICE_NAME" envDefault:"{{APP_NAME}}"`
	Addr        string `env:"ADDR" envDefault:":8080"`
	LogLevel    string `env:"LOG_LEVEL" envDefault:"info"`

	AppVersion    string `env:"APP_VERSION" envDefault:"0.1.0"`
	DocsEnabled   bool   `env:"DOCS_ENABLED" envDefault:"true"`
	PublicBaseURL string `env:"PUBLIC_BASE_URL"`

	DatabaseURL       string `env:"DATABASE_URL,required,notEmpty"`
	DirectDatabaseURL string `env:"DIRECT_DATABASE_URL"`

	TrustedProxies []string `env:"TRUSTED_PROXIES" envSeparator:","`

	AuthTokenKey    string        `env:"AUTH_TOKEN_KEY,required,notEmpty"`
	AccessTokenTTL  time.Duration `env:"ACCESS_TOKEN_TTL" envDefault:"15m"`
	RefreshTokenTTL time.Duration `env:"REFRESH_TOKEN_TTL" envDefault:"720h"`

	Argon2MemoryKiB uint32 `env:"ARGON2_MEMORY_KIB" envDefault:"65536"`
	Argon2Time      uint32 `env:"ARGON2_TIME" envDefault:"3"`
	Argon2Threads   uint8  `env:"ARGON2_THREADS" envDefault:"2"`

	RateLimitRPS        float64 `env:"RATE_LIMIT_RPS" envDefault:"20"`
	RateLimitBurst      int     `env:"RATE_LIMIT_BURST" envDefault:"40"`
	AuthRateLimitPerMin int     `env:"AUTH_RATE_LIMIT_PER_MIN" envDefault:"5"`

	// template:example:begin acme
	WebhookAcmeSecret string `env:"WEBHOOK_ACME_SECRET"`
	// template:example:end acme

	OtelEndpoint string `env:"OTEL_EXPORTER_OTLP_ENDPOINT"`

	WorkerConcurrency int `env:"WORKER_CONCURRENCY" envDefault:"10"`
}

// Load reads the process environment, applies defaults, and validates every
// field. The returned error lists every invalid field, not just the first.
func Load() (Config, error) {
	return parse(env.Options{})
}

// LoadFrom is Load with an explicit environment, for tests.
func LoadFrom(environ map[string]string) (Config, error) {
	return parse(env.Options{Environment: environ})
}

func parse(opts env.Options) (Config, error) {
	var cfg Config
	if err := env.ParseWithOptions(&cfg, opts); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	cfg.normalize()
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}

func (c *Config) normalize() {
	proxies := make([]string, 0, len(c.TrustedProxies))
	for _, p := range c.TrustedProxies {
		if p = strings.TrimSpace(p); p != "" {
			proxies = append(proxies, p)
		}
	}
	c.TrustedProxies = proxies
	if c.DirectDatabaseURL == "" {
		c.DirectDatabaseURL = c.DatabaseURL
	}
}

func (c Config) validate() error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	switch c.Env {
	case EnvLocal, EnvStaging, EnvProduction:
	default:
		add("ENV: must be one of local, staging, production, got %q", c.Env)
	}
	if c.Addr == "" {
		add("ADDR: must not be empty")
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(c.LogLevel)); err != nil {
		add("LOG_LEVEL: %v", err)
	}
	for _, cidr := range c.TrustedProxies {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			add("TRUSTED_PROXIES: %q is not a CIDR", cidr)
		}
	}
	if _, err := c.TokenKey(); err != nil {
		errs = append(errs, err)
	}
	if c.AccessTokenTTL <= 0 {
		add("ACCESS_TOKEN_TTL: must be positive")
	}
	if c.RefreshTokenTTL <= c.AccessTokenTTL {
		add("REFRESH_TOKEN_TTL: must be longer than ACCESS_TOKEN_TTL")
	}
	if c.Argon2Time == 0 {
		add("ARGON2_TIME: must be at least 1")
	}
	if c.Argon2Threads == 0 {
		add("ARGON2_THREADS: must be at least 1")
	}
	if c.Argon2MemoryKiB < 8*uint32(c.Argon2Threads) {
		add("ARGON2_MEMORY_KIB: must be at least 8 times ARGON2_THREADS")
	}
	if c.RateLimitRPS <= 0 {
		add("RATE_LIMIT_RPS: must be positive")
	}
	if c.RateLimitBurst < 1 {
		add("RATE_LIMIT_BURST: must be at least 1")
	}
	if c.AuthRateLimitPerMin < 1 {
		add("AUTH_RATE_LIMIT_PER_MIN: must be at least 1")
	}
	if c.WorkerConcurrency < 1 {
		add("WORKER_CONCURRENCY: must be at least 1")
	}
	if c.OtelEndpoint != "" {
		u, err := url.Parse(c.OtelEndpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			add("OTEL_EXPORTER_OTLP_ENDPOINT: must be an http or https URL")
		}
	}
	return errors.Join(errs...)
}

// TokenKey decodes AUTH_TOKEN_KEY into the 32 raw key bytes. Errors never
// include the key itself.
func (c Config) TokenKey() ([]byte, error) {
	key, err := hex.DecodeString(c.AuthTokenKey)
	if err != nil {
		return nil, errors.New("AUTH_TOKEN_KEY: must be hex encoded")
	}
	if len(key) != tokenKeyBytes {
		return nil, fmt.Errorf("AUTH_TOKEN_KEY: must be %d hex characters (%d bytes), got %d bytes",
			tokenKeyBytes*2, tokenKeyBytes, len(key))
	}
	return key, nil
}

// LogValue implements slog.LogValuer. It exposes non-secret settings only, so
// logging a Config never leaks database URLs, keys, or webhook secrets.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("env", c.Env),
		slog.String("service_name", c.ServiceName),
		slog.String("addr", c.Addr),
		slog.String("log_level", c.LogLevel),
		slog.Int("trusted_proxies", len(c.TrustedProxies)),
		slog.Bool("otel_enabled", c.OtelEndpoint != ""),
		slog.Int("worker_concurrency", c.WorkerConcurrency),
		slog.Float64("rate_limit_rps", c.RateLimitRPS),
		slog.Int("rate_limit_burst", c.RateLimitBurst),
	)
}
