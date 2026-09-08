package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultListenAddress     = ":8080"
	defaultReadTimeout       = 15 * time.Second
	defaultReadHeaderTimeout = 5 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 60 * time.Second
	defaultShutdownTimeout   = 20 * time.Second
	defaultMaxHeaderBytes    = 1 << 20
)

type Config struct {
	ListenAddress     string
	Environment       string
	LogLevel          string
	ResourcesFile     string
	DatabaseURL       string
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	MaxHeaderBytes    int
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddress:     envOrDefault("CC_LISTEN_ADDR", defaultListenAddress),
		Environment:       strings.ToLower(envOrDefault("CC_ENVIRONMENT", "development")),
		LogLevel:          strings.ToLower(envOrDefault("CC_LOG_LEVEL", "info")),
		ResourcesFile:     strings.TrimSpace(os.Getenv("CC_RESOURCES_FILE")),
		DatabaseURL:       strings.TrimSpace(os.Getenv("CC_DATABASE_URL")),
		ReadTimeout:       defaultReadTimeout,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		WriteTimeout:      defaultWriteTimeout,
		IdleTimeout:       defaultIdleTimeout,
		ShutdownTimeout:   defaultShutdownTimeout,
		MaxHeaderBytes:    defaultMaxHeaderBytes,
	}

	var err error
	if cfg.ReadTimeout, err = durationFromEnv("CC_READ_TIMEOUT", cfg.ReadTimeout); err != nil {
		return Config{}, err
	}
	if cfg.ReadHeaderTimeout, err = durationFromEnv("CC_READ_HEADER_TIMEOUT", cfg.ReadHeaderTimeout); err != nil {
		return Config{}, err
	}
	if cfg.WriteTimeout, err = durationFromEnv("CC_WRITE_TIMEOUT", cfg.WriteTimeout); err != nil {
		return Config{}, err
	}
	if cfg.IdleTimeout, err = durationFromEnv("CC_IDLE_TIMEOUT", cfg.IdleTimeout); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = durationFromEnv("CC_SHUTDOWN_TIMEOUT", cfg.ShutdownTimeout); err != nil {
		return Config{}, err
	}
	if cfg.MaxHeaderBytes, err = intFromEnv("CC_MAX_HEADER_BYTES", cfg.MaxHeaderBytes); err != nil {
		return Config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.ListenAddress) == "" {
		return errors.New("CC_LISTEN_ADDR must not be empty")
	}
	if strings.TrimSpace(c.DatabaseURL) == "" {
		return errors.New("CC_DATABASE_URL must not be empty")
	}
	validEnvironments := map[string]bool{"development": true, "test": true, "staging": true, "production": true}
	if !validEnvironments[c.Environment] {
		return fmt.Errorf("CC_ENVIRONMENT must be one of development, test, staging, production: %q", c.Environment)
	}
	validLogLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLogLevels[c.LogLevel] {
		return fmt.Errorf("CC_LOG_LEVEL must be one of debug, info, warn, error: %q", c.LogLevel)
	}
	if c.ReadTimeout <= 0 || c.ReadHeaderTimeout <= 0 || c.WriteTimeout <= 0 || c.IdleTimeout <= 0 || c.ShutdownTimeout <= 0 {
		return errors.New("HTTP and shutdown timeouts must be greater than zero")
	}
	if c.MaxHeaderBytes < 4096 || c.MaxHeaderBytes > 16<<20 {
		return fmt.Errorf("CC_MAX_HEADER_BYTES must be between 4096 and 16777216: %d", c.MaxHeaderBytes)
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func durationFromEnv(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return value, nil
}

func intFromEnv(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return value, nil
}
