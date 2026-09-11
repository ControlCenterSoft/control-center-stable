package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"control-center/internal/resources"
)

type failingReadiness struct{}

func (failingReadiness) PingContext(context.Context) error { return errors.New("database unavailable") }
func testHandler(t *testing.T) http.Handler {
	t.Helper()
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	registry, err := resources.NewMemoryRegistry([]resources.Resource{{ID: "node-1", OrganizationID: "org-1", Kind: "node", Name: "Node 1", Status: "ready", Revision: 1, CreatedAt: now, UpdatedAt: now}})
	if err != nil {
		t.Fatalf("NewMemoryRegistry() error = %v", err)
	}
	allowResourceRead := func(next http.Handler) http.Handler { return next }
	return New(slog.New(slog.NewTextHandler(io.Discard, nil)), registry, WithResourceGuard(allowResourceRead)).Handler()
}
func TestResourceReadFailsClosedWithoutGuard(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	registry, err := resources.NewMemoryRegistry([]resources.Resource{{ID: "node-1", OrganizationID: "org-1", Kind: "node", Name: "Node 1", Status: "ready", Revision: 1, CreatedAt: now, UpdatedAt: now}})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(slog.New(slog.NewTextHandler(io.Discard, nil)), registry).Handler()
	for _, path := range []string{"/api/v1/resources", "/api/v1/resources/node-1"} {
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, httptest.NewRequest(http.MethodGet, path, nil))
		if result.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s status = %d, want 401; body=%s", path, result.Code, result.Body.String())
		}
	}
}
func TestHealthAndVersion(t *testing.T) {
	handler := testHandler(t)
	for _, path := range []string{"/health/live", "/health/ready", "/api/v1/version"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200; body=%s", path, recorder.Code, recorder.Body.String())
		}
		if recorder.Header().Get(correlationHeader) == "" {
			t.Fatalf("GET %s missing correlation header", path)
		}
	}
}
func TestReadinessFailsClosedWhenDatabaseIsUnavailable(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	registry, err := resources.NewMemoryRegistry([]resources.Resource{{ID: "node-1", OrganizationID: "org-1", Kind: "node", Name: "Node 1", Status: "ready", Revision: 1, CreatedAt: now, UpdatedAt: now}})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(slog.New(slog.NewTextHandler(io.Discard, nil)), registry, WithReadinessCheck(failingReadiness{})).Handler()
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if result.Code != http.StatusServiceUnavailable || !strings.Contains(result.Body.String(), `"code":"SERVICE_NOT_READY"`) {
		t.Fatalf("readiness status=%d body=%s", result.Code, result.Body.String())
	}
}
func TestResourceReadAPI(t *testing.T) {
	handler := testHandler(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/resources?organization_id=org-1&kind=node", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var list struct {
		Items []resources.Resource `json:"items"`
		Count int                  `json:"count"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Count != 1 || len(list.Items) != 1 || list.Items[0].ID != "node-1" {
		t.Fatalf("unexpected list: %#v", list)
	}
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/v1/resources/node-1", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"id":"node-1"`) {
		t.Fatalf("get response = %d %s", recorder.Code, recorder.Body.String())
	}
}
func TestStableErrorEnvelopeAndCorrelationID(t *testing.T) {
	handler := testHandler(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/resources/missing", nil)
	request.Header.Set(correlationHeader, "test-correlation-42")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
	if recorder.Header().Get(correlationHeader) != "test-correlation-42" {
		t.Fatalf("correlation header = %q", recorder.Header().Get(correlationHeader))
	}
	var envelope errorEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if envelope.Error.Code != "RESOURCE_NOT_FOUND" || envelope.Error.CorrelationID != "test-correlation-42" {
		t.Fatalf("unexpected error envelope: %#v", envelope)
	}
}
func TestWritesAreRejected(t *testing.T) {
	handler := testHandler(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/resources", strings.NewReader(`{}`))
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", recorder.Code)
	}
	if recorder.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("Allow = %q, want GET", recorder.Header().Get("Allow"))
	}
}
func TestUnknownQueryIsRejected(t *testing.T) {
	handler := testHandler(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/resources?unexpected=true", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}
