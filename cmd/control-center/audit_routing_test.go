package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSplitHandlerRoutesAuditBrowserPathToIdentity(t *testing.T) {
	identityCalls := 0
	coreCalls := 0
	handler := splitHandler{
		core: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			coreCalls++
			w.WriteHeader(http.StatusTeapot)
		}),
		identity: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			identityCalls++
			w.WriteHeader(http.StatusNoContent)
		}),
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/audit", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d want=%d", recorder.Code, http.StatusNoContent)
	}
	if identityCalls != 1 || coreCalls != 0 {
		t.Fatalf("identityCalls=%d coreCalls=%d, want 1/0", identityCalls, coreCalls)
	}
}

func TestSplitHandlerDoesNotCaptureAuditLookalikePath(t *testing.T) {
	identityCalls := 0
	coreCalls := 0
	handler := splitHandler{
		core: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			coreCalls++
			w.WriteHeader(http.StatusTeapot)
		}),
		identity: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			identityCalls++
			w.WriteHeader(http.StatusNoContent)
		}),
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/audit-export", nil))
	if recorder.Code != http.StatusTeapot {
		t.Fatalf("status=%d want=%d", recorder.Code, http.StatusTeapot)
	}
	if identityCalls != 0 || coreCalls != 1 {
		t.Fatalf("identityCalls=%d coreCalls=%d, want 0/1", identityCalls, coreCalls)
	}
}
