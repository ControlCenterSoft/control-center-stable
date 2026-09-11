package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	keys := []string{
		"CC_LISTEN_ADDR", "CC_ENVIRONMENT", "CC_LOG_LEVEL", "CC_RESOURCES_FILE",
		"CC_DATABASE_URL",
		"CC_READ_TIMEOUT", "CC_READ_HEADER_TIMEOUT", "CC_WRITE_TIMEOUT",
		"CC_IDLE_TIMEOUT", "CC_SHUTDOWN_TIMEOUT", "CC_MAX_HEADER_BYTES",
		"CC_AUTH_SESSION_TTL", "CC_AUTH_SESSION_IDLE_TIMEOUT",
	}
	for _, key := range keys {
		t.Setenv(key, "")
	}
	t.Setenv("CC_DATABASE_URL", "postgres://test:test@localhost/test?sslmode=disable")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ListenAddress != ":8080" {
		t.Fatalf("ListenAddress = %q, want :8080", cfg.ListenAddress)
	}
	if cfg.Environment != "development" {
		t.Fatalf("Environment = %q, want development", cfg.Environment)
	}
	if cfg.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("ReadHeaderTimeout = %s, want 5s", cfg.ReadHeaderTimeout)
	}
	if cfg.AuthSessionTTL != 8*time.Hour || cfg.AuthSessionIdleTimeout != 2*time.Hour {
		t.Fatalf("session policy ttl=%s idle=%s, want 8h/2h", cfg.AuthSessionTTL, cfg.AuthSessionIdleTimeout)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("CC_LISTEN_ADDR", "127.0.0.1:9090")
	t.Setenv("CC_ENVIRONMENT", "STAGING")
	t.Setenv("CC_LOG_LEVEL", "DEBUG")
	t.Setenv("CC_RESOURCES_FILE", "/data/resources.json")
	t.Setenv("CC_DATABASE_URL", "postgres://app:synthetic@database/control_center?sslmode=disable")
	t.Setenv("CC_READ_TIMEOUT", "3s")
	t.Setenv("CC_READ_HEADER_TIMEOUT", "2s")
	t.Setenv("CC_WRITE_TIMEOUT", "4s")
	t.Setenv("CC_IDLE_TIMEOUT", "45s")
	t.Setenv("CC_SHUTDOWN_TIMEOUT", "12s")
	t.Setenv("CC_MAX_HEADER_BYTES", "8192")
	t.Setenv("CC_AUTH_SESSION_TTL", "12h")
	t.Setenv("CC_AUTH_SESSION_IDLE_TIMEOUT", "30m")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Environment != "staging" || cfg.LogLevel != "debug" {
		t.Fatalf("normalization failed: environment=%q log_level=%q", cfg.Environment, cfg.LogLevel)
	}
	if cfg.ReadTimeout != 3*time.Second || cfg.MaxHeaderBytes != 8192 {
		t.Fatalf("overrides failed: read_timeout=%s max_header_bytes=%d", cfg.ReadTimeout, cfg.MaxHeaderBytes)
	}
	if cfg.AuthSessionTTL != 12*time.Hour || cfg.AuthSessionIdleTimeout != 30*time.Minute {
		t.Fatalf("session overrides ttl=%s idle=%s", cfg.AuthSessionTTL, cfg.AuthSessionIdleTimeout)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	t.Setenv("CC_READ_TIMEOUT", "not-a-duration")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid duration error")
	}
}

func TestLoadRejectsUnsafeSessionPolicy(t *testing.T) {
	t.Setenv("CC_DATABASE_URL", "postgres://test:test@localhost/test?sslmode=disable")
	t.Setenv("CC_AUTH_SESSION_TTL", "30m")
	t.Setenv("CC_AUTH_SESSION_IDLE_TIMEOUT", "45m")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want idle timeout greater than absolute TTL to fail")
	}

	t.Setenv("CC_AUTH_SESSION_TTL", "8d")
	t.Setenv("CC_AUTH_SESSION_IDLE_TIMEOUT", "2h")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want session TTL above 7d to fail")
	}
}

func TestValidateRejectsUnsafeHeaderLimit(t *testing.T) {
	cfg := Config{
		ListenAddress:          ":8080",
		Environment:            "production",
		LogLevel:               "info",
		DatabaseURL:            "postgres://test:test@localhost/test?sslmode=disable",
		ReadTimeout:            time.Second,
		ReadHeaderTimeout:      time.Second,
		WriteTimeout:           time.Second,
		IdleTimeout:            time.Second,
		ShutdownTimeout:        time.Second,
		MaxHeaderBytes:         1024,
		AuthSessionTTL:         8 * time.Hour,
		AuthSessionIdleTimeout: 2 * time.Hour,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want header limit error")
	}
}

func TestLoadRejectsMissingDatabaseURL(t *testing.T) {
	t.Setenv("CC_DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want missing database URL error")
	}
}
