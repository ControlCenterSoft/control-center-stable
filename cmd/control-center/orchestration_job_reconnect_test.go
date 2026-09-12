package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJobReconnectReadExposesStrongVersionETagWithoutCaching(t *testing.T) {
	handler := jobReconnectETagMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"job-1","version":7,"status":"running"}`))
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-1", nil)
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)

	if result.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	if result.Header().Get("ETag") != `"7"` {
		t.Fatalf("etag=%q", result.Header().Get("ETag"))
	}
	if result.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control=%q", result.Header().Get("Cache-Control"))
	}
	if !strings.Contains(result.Body.String(), `"version":7`) {
		t.Fatalf("body changed unexpectedly: %s", result.Body.String())
	}
}

func TestJobReconnectMiddlewareDoesNotLeakVersionFromDeniedRead(t *testing.T) {
	handler := jobReconnectETagMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"permission_denied"},"version":99}`))
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-secret", nil)
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)

	if result.Code != http.StatusForbidden {
		t.Fatalf("status=%d", result.Code)
	}
	if result.Header().Get("ETag") != "" {
		t.Fatalf("denied response leaked durable version through etag=%q", result.Header().Get("ETag"))
	}
}

func TestJobReconnectMiddlewareIgnoresNonJobReads(t *testing.T) {
	handler := jobReconnectETagMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":8}`))
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/actions", nil)
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)

	if result.Header().Get("ETag") != "" || result.Header().Get("Cache-Control") != "" {
		t.Fatalf("non-job response was modified: headers=%v", result.Header())
	}
}
