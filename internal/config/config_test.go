package config

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func validEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":   "postgres://user:s3cret@localhost:5432/db",
		"AUTH_TOKEN_KEY": strings.Repeat("ab", 32),
	}
}

func TestLoadFromDefaults(t *testing.T) {
	cfg, err := LoadFrom(validEnv())
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Env != EnvLocal {
		t.Errorf("Env = %q, want %q", cfg.Env, EnvLocal)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.AccessTokenTTL != 15*time.Minute {
		t.Errorf("AccessTokenTTL = %v, want 15m", cfg.AccessTokenTTL)
	}
	if cfg.RefreshTokenTTL != 720*time.Hour {
		t.Errorf("RefreshTokenTTL = %v, want 720h", cfg.RefreshTokenTTL)
	}
	if cfg.Argon2MemoryKiB != 65536 || cfg.Argon2Time != 3 || cfg.Argon2Threads != 2 {
		t.Errorf("argon2 defaults = %d/%d/%d", cfg.Argon2MemoryKiB, cfg.Argon2Time, cfg.Argon2Threads)
	}
	if cfg.AuthRateLimitPerMin != 5 {
		t.Errorf("AuthRateLimitPerMin = %d, want 5", cfg.AuthRateLimitPerMin)
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Errorf("TrustedProxies = %v, want none", cfg.TrustedProxies)
	}
}

func TestDirectDatabaseURLFallsBack(t *testing.T) {
	cfg, err := LoadFrom(validEnv())
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.DirectDatabaseURL != cfg.DatabaseURL {
		t.Errorf("DirectDatabaseURL = %q, want fallback to DatabaseURL", cfg.DirectDatabaseURL)
	}

	environ := validEnv()
	environ["DIRECT_DATABASE_URL"] = "postgres://direct@localhost/db"
	cfg, err = LoadFrom(environ)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.DirectDatabaseURL != "postgres://direct@localhost/db" {
		t.Errorf("DirectDatabaseURL = %q, want explicit value", cfg.DirectDatabaseURL)
	}
}

func TestMissingRequiredListsEveryField(t *testing.T) {
	_, err := LoadFrom(map[string]string{})
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, field := range []string{"DATABASE_URL", "AUTH_TOKEN_KEY"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("error %q does not mention %s", err, field)
		}
	}
}

func TestInvalidValuesListEveryField(t *testing.T) {
	environ := validEnv()
	environ["ENV"] = "prod"
	environ["LOG_LEVEL"] = "loud"
	environ["TRUSTED_PROXIES"] = "10.0.0.0/8, not-a-cidr"
	environ["AUTH_TOKEN_KEY"] = "short"
	environ["OTEL_EXPORTER_OTLP_ENDPOINT"] = "collector:4317"

	_, err := LoadFrom(environ)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, field := range []string{"ENV", "LOG_LEVEL", "TRUSTED_PROXIES", "AUTH_TOKEN_KEY", "OTEL_EXPORTER_OTLP_ENDPOINT"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("error %q does not mention %s", err, field)
		}
	}
	if strings.Contains(err.Error(), "short") {
		t.Errorf("error %q leaks the token key value", err)
	}
}

func TestTrustedProxiesAreTrimmed(t *testing.T) {
	environ := validEnv()
	environ["TRUSTED_PROXIES"] = " 10.0.0.0/8 , ,192.168.0.0/16"
	cfg, err := LoadFrom(environ)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(cfg.TrustedProxies) != 2 {
		t.Fatalf("TrustedProxies = %v, want 2 entries", cfg.TrustedProxies)
	}
}

func TestTokenKey(t *testing.T) {
	cfg, err := LoadFrom(validEnv())
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	key, err := cfg.TokenKey()
	if err != nil {
		t.Fatalf("TokenKey: %v", err)
	}
	if len(key) != 32 {
		t.Errorf("len(key) = %d, want 32", len(key))
	}
}

func TestLogValueHidesSecrets(t *testing.T) {
	cfg, err := LoadFrom(validEnv())
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("loaded", "config", cfg)

	out := buf.String()
	if strings.Contains(out, "s3cret") {
		t.Errorf("log output leaks the database password: %s", out)
	}
	if strings.Contains(out, strings.Repeat("ab", 32)) {
		t.Errorf("log output leaks the token key: %s", out)
	}
	if !strings.Contains(out, "service_name") {
		t.Errorf("log output is missing non-secret fields: %s", out)
	}
}

func TestScalingSettings(t *testing.T) {
	tests := []struct {
		name    string
		extra   map[string]string
		wantErr string
	}{
		{name: "defaults"},
		{name: "queues", extra: map[string]string{"WORKER_QUEUES": "default:10,webhooks:5"}},
		{name: "zero queue", extra: map[string]string{"WORKER_QUEUES": "slow:0"}, wantErr: "WORKER_QUEUES"},
		{name: "min above max", extra: map[string]string{"DB_MAX_CONNS": "5", "DB_MIN_CONNS": "9"}, wantErr: "DB_MAX_CONNS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validEnv()
			for k, v := range tt.extra {
				env[k] = v
			}
			cfg, err := LoadFrom(env)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to mention %s", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadFrom: %v", err)
			}
			if cfg.WorkerHealthAddr != ":8081" || cfg.DBMaxConnLifetime != 30*time.Minute {
				t.Errorf("defaults = %q/%v", cfg.WorkerHealthAddr, cfg.DBMaxConnLifetime)
			}
			if tt.name == "queues" && (cfg.WorkerQueues["webhooks"] != 5 || len(cfg.WorkerQueues) != 2) {
				t.Errorf("WorkerQueues = %v", cfg.WorkerQueues)
			}
		})
	}
}
