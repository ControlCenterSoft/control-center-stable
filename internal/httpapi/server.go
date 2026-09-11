package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"control-center/internal/buildinfo"
	"control-center/internal/resources"
)

const correlationHeader = "X-Correlation-ID"

type Server struct {
	logger        *slog.Logger
	resources     resources.Reader
	resourceGuard func(http.Handler) http.Handler
	readiness     []interface{ PingContext(context.Context) error }
	handler       http.Handler
}

type Option func(*Server)

func WithResourceGuard(guard func(http.Handler) http.Handler) Option {
	return func(s *Server) { s.resourceGuard = guard }
}

func WithReadinessCheck(check interface{ PingContext(context.Context) error }) Option {
	return func(s *Server) {
		if check != nil {
			s.readiness = append(s.readiness, check)
		}
	}
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}
type apiError struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	CorrelationID string `json:"correlation_id"`
}
type contextKey string

const correlationIDKey contextKey = "correlation_id"

var validCorrelationID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func New(logger *slog.Logger, resourceReader resources.Reader, options ...Option) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{logger: logger, resources: resourceReader}
	for _, option := range options {
		if option != nil {
			option(s)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", s.live)
	mux.HandleFunc("/health/ready", s.ready)
	mux.HandleFunc("/api/v1/version", s.version)
	mux.Handle("/api/v1/resources", s.protectResourceRead(http.HandlerFunc(s.listResources)))
	mux.Handle("/api/v1/resources/", s.protectResourceRead(http.HandlerFunc(s.getResource)))
	mux.HandleFunc("/", s.notFound)
	s.handler = s.correlationID(s.recoverPanic(s.accessLog(s.securityHeaders(mux))))
	return s
}

func (s *Server) protectResourceRead(next http.Handler) http.Handler {
	if s.resourceGuard != nil {
		return s.resourceGuard(next)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "authentication is required")
	})
}
func (s *Server) Handler() http.Handler { return s.handler }
func Middleware(logger *slog.Logger, next http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{logger: logger}
	return s.correlationID(s.recoverPanic(s.accessLog(s.securityHeaders(next))))
}
func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	if !allowGET(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if !allowGET(w, r) {
		return
	}
	if s.resources == nil {
		writeError(w, r, http.StatusServiceUnavailable, "SERVICE_NOT_READY", "resource registry is not initialized")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.resources.Ready(ctx); err != nil {
		s.logger.WarnContext(r.Context(), "readiness check failed", "error", err)
		writeError(w, r, http.StatusServiceUnavailable, "SERVICE_NOT_READY", "service dependencies are not ready")
		return
	}
	for _, check := range s.readiness {
		if err := check.PingContext(ctx); err != nil {
			s.logger.WarnContext(r.Context(), "readiness dependency failed", "error", err)
			writeError(w, r, http.StatusServiceUnavailable, "SERVICE_NOT_READY", "service dependencies are not ready")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
func (s *Server) version(w http.ResponseWriter, r *http.Request) {
	if !allowGET(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": buildinfo.ProductName, "version": buildinfo.Version, "api_version": buildinfo.APIVersion, "commit": buildinfo.Commit, "build_time": buildinfo.BuildTime})
}
func (s *Server) listResources(w http.ResponseWriter, r *http.Request) {
	if !allowGET(w, r) {
		return
	}
	if s.resources == nil {
		writeError(w, r, http.StatusServiceUnavailable, "SERVICE_NOT_READY", "resource registry is not initialized")
		return
	}
	if err := rejectUnknownQuery(r, "organization_id", "kind"); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_QUERY", err.Error())
		return
	}
	filter := resources.Filter{OrganizationID: strings.TrimSpace(r.URL.Query().Get("organization_id")), Kind: strings.TrimSpace(r.URL.Query().Get("kind"))}
	items, err := s.resources.List(r.Context(), filter)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "list resources", "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "unable to read resources")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Items []resources.Resource `json:"items"`
		Count int                  `json:"count"`
	}{Items: items, Count: len(items)})
}
func (s *Server) getResource(w http.ResponseWriter, r *http.Request) {
	if !allowGET(w, r) {
		return
	}
	if s.resources == nil {
		writeError(w, r, http.StatusServiceUnavailable, "SERVICE_NOT_READY", "resource registry is not initialized")
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, r, http.StatusBadRequest, "INVALID_QUERY", "query parameters are not supported for this endpoint")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/resources/")
	if id == "" || strings.Contains(id, "/") || len(id) > 128 {
		writeError(w, r, http.StatusBadRequest, "INVALID_RESOURCE_ID", "resource id must be a single non-empty path segment")
		return
	}
	resource, err := s.resources.Get(r.Context(), id)
	if errors.Is(err, resources.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "resource was not found")
		return
	}
	if err != nil {
		s.logger.ErrorContext(r.Context(), "get resource", "resource_id", id, "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "unable to read resource")
		return
	}
	writeJSON(w, http.StatusOK, resource)
}
func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusNotFound, "ROUTE_NOT_FOUND", "route was not found")
}
func allowGET(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet {
		return true
	}
	w.Header().Set("Allow", http.MethodGet)
	writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method is not allowed")
	return false
}
func rejectUnknownQuery(r *http.Request, allowed ...string) error {
	allowedKeys := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		allowedKeys[key] = true
	}
	for key := range r.URL.Query() {
		if !allowedKeys[key] {
			return errors.New("unsupported query parameter: " + key)
		}
	}
	return nil
}
func (s *Server) correlationID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		correlationID := r.Header.Get(correlationHeader)
		if !validCorrelationID.MatchString(correlationID) {
			correlationID = newCorrelationID()
		}
		w.Header().Set(correlationHeader, correlationID)
		ctx := context.WithValue(r.Context(), correlationIDKey, correlationID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.ErrorContext(r.Context(), "panic recovered", "panic", recovered, "stack", string(debug.Stack()))
				writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "an internal error occurred")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.logger.InfoContext(r.Context(), "http request", "method", r.Method, "path", r.URL.Path, "status", recorder.status, "duration_ms", time.Since(start).Milliseconds(), "correlation_id", correlationIDFromContext(r.Context()))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	WriteError(w, r, status, code, message)
}
func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	correlationID := w.Header().Get(correlationHeader)
	if r != nil {
		correlationID = correlationIDFromContext(r.Context())
	}
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: message, CorrelationID: correlationID}})
}
func CorrelationID(ctx context.Context) string { return correlationIDFromContext(ctx) }
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(true)
	_ = encoder.Encode(value)
}
func correlationIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(correlationIDKey).(string)
	return value
}
func newCorrelationID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format("20060102150405.000000000")))
	}
	return hex.EncodeToString(value[:])
}
