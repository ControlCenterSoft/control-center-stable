package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"control-center/internal/buildinfo"
	"control-center/internal/config"
	"control-center/internal/httpapi"
	"control-center/internal/identity/rbac"
	"control-center/internal/persistence/postgres"
	"control-center/internal/resources"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)
	startupContext, cancelStartup := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelStartup()
	db, err := postgres.Open(startupContext, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	initialResources, err := resources.LoadSnapshot(cfg.ResourcesFile)
	if err != nil {
		return err
	}
	registry, err := resources.NewMemoryRegistry(initialResources)
	if err != nil {
		return fmt.Errorf("initialize resource registry: %w", err)
	}

	identity, err := newIdentityHandler(cfg.Environment, db,
		strings.TrimSpace(os.Getenv("CC_BOOTSTRAP_ADMIN_USERNAME")), os.Getenv("CC_BOOTSTRAP_ADMIN_PASSWORD"))
	if err != nil {
		return fmt.Errorf("initialize identity: %w", err)
	}
	resourceGuard := func(next http.Handler) http.Handler {
		return identity.Authenticate(identity.Require(rbac.PermissionResourcesRead, rbac.GlobalScope())(next))
	}
	api := httpapi.New(logger, registry, httpapi.WithResourceGuard(resourceGuard), httpapi.WithReadinessCheck(db))
	commonMiddleware := func(next http.Handler) http.Handler { return httpapi.Middleware(logger, next) }
	orchestration, runner, err := newOrchestrationHandler(identity, db, commonMiddleware)
	if err != nil {
		return fmt.Errorf("initialize orchestration: %w", err)
	}
	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           splitHandler{core: api.Handler(), identity: commonMiddleware(identity), orchestration: orchestration.Handler()},
		ReadTimeout:       cfg.ReadTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
	}
	shutdownSignal, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go runWorker(shutdownSignal, logger, orchestration, runner)

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("control center starting",
			"address", cfg.ListenAddress,
			"environment", cfg.Environment,
			"version", buildinfo.Version,
			"commit", buildinfo.Commit,
		)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-shutdownSignal.Done():
		logger.Info("shutdown requested")
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		_ = server.Close()
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("shutdown complete")
	return nil
}

type splitHandler struct {
	core          http.Handler
	identity      http.Handler
	orchestration http.Handler
}

func (h splitHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.HasPrefix(path, "/api/v1/auth/") ||
		strings.HasPrefix(path, "/api/v1/identity/") ||
		path == "/api/v1/system/overview" ||
		path == "/login" || path == "/overview" ||
		strings.HasPrefix(path, "/web/") {
		h.identity.ServeHTTP(w, r)
		return
	}
	if path == "/api/v1/actions" ||
		strings.HasPrefix(path, "/api/v1/config/") ||
		strings.HasPrefix(path, "/api/v1/changes") ||
		strings.HasPrefix(path, "/api/v1/jobs/") {
		h.orchestration.ServeHTTP(w, r)
		return
	}
	h.core.ServeHTTP(w, r)
}

func newLogger(level string) *slog.Logger {
	var slogLevel slog.Level
	switch level {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slogLevel}))
}
