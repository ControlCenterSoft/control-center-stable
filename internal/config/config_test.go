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
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("CC_LISTEN_ADDR", "127.0.0.1:9090")
	t.Setenv("CC_ENVIRONMENT", "STAGING")
	t.Setenv("CC_LOG_LEVEL", "DEBUG")
	t.Setenv("CC_RESOURCES_FILE", "/data/resources.json")
	t.Setenv("CC_DATABASE_URL", "postgres://app:secret@database/control_center?sslmode=disable")
	t.Setenv("CC_READ_TIMEOUT", "3s")
	t.Setenv("CC_READ_HEADER_TIMEOUT", "2s")
	t.Setenv("CC_WRITE_TIMEOUT", "4s")
	t.Setenv("CC_IDLE_TIMEOUT", "45s")
	t.Setenv("CC_SHUTDOWN_TIMEOUT", "12s")
	t.Setenv("CC_MAX_HEADER_BYTES", "8192")

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
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	t.Setenv("CC_READ_TIMEOUT", "not-a-duration")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid duration error")
	}
}

func TestValidateRejectsUnsafeHeaderLimit(t *testing.T) {
	cfg := Config{
		ListenAddress:     ":8080",
		Environment:       "production",
		LogLevel:          "info",
		DatabaseURL:       "postgres://test:test@localhost/test?sslmode=disable",
		ReadTimeout:       time.Second,
		ReadHeaderTimeout: time.Second,
		WriteTimeout:      time.Second,
		IdleTimeout:       time.Second,
		ShutdownTimeout:   time.Second,
		MaxHeaderBytes:    1024,
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
