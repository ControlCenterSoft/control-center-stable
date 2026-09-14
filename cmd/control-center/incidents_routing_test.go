package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSplitHandlerRoutesIncidentCollectionAndObjectPaths(t *testing.T) {
	incidentCalls := 0
	coreCalls := 0
	handler := splitHandler{
		core: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			coreCalls++
			w.WriteHeader(http.StatusNotFound)
		}),
		incidents: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			incidentCalls++
			w.WriteHeader(http.StatusNoContent)
		}),
	}

	for _, path := range []string{"/api/v1/incidents", "/api/v1/incidents/incident-1", "/api/v1/incidents/incident-1/acknowledge"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("%s status = %d, want %d", path, recorder.Code, http.StatusNoContent)
		}
	}
	if incidentCalls != 3 || coreCalls != 0 {
		t.Fatalf("incidentCalls=%d coreCalls=%d, want 3/0", incidentCalls, coreCalls)
	}
}

func TestSplitHandlerDoesNotCaptureIncidentLookalikePath(t *testing.T) {
	incidentCalls := 0
	coreCalls := 0
	handler := splitHandler{
		core: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			coreCalls++
			w.WriteHeader(http.StatusTeapot)
		}),
		incidents: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			incidentCalls++
			w.WriteHeader(http.StatusNoContent)
		}),
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/incidents-export", nil))
	if recorder.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTeapot)
	}
	if incidentCalls != 0 || coreCalls != 1 {
		t.Fatalf("incidentCalls=%d coreCalls=%d, want 0/1", incidentCalls, coreCalls)
	}
}
