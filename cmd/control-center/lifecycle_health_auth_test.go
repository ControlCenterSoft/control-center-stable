package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLifecycleHealthAPIRBAC(t *testing.T) {
	fixture := newResourceAuthFixture(t)
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "inventory reconcile", path: "/api/v1/inventory/reconcile", body: `{"observations":[{"device_id":"dev-1","source":"agent","seen_at":"2026-09-08T10:00:00Z"}]}`},
		{name: "inventory freshness", path: "/api/v1/inventory/freshness", body: `{"observed_at":"2026-09-08T10:00:00Z","now":"2026-09-08T10:10:00Z","stale_after_seconds":300,"expire_after_seconds":900}`},
		{name: "agent heartbeat", path: "/api/v1/agent/heartbeat/evaluate", body: `{"last_seen":"2026-09-08T10:00:00Z","now":"2026-09-08T10:10:00Z","delayed_after_seconds":300,"offline_after_seconds":900}`},
		{name: "agent lease", path: "/api/v1/agent/lease/evaluate", body: `{"node_id":"node-1","last_heartbeat":"2026-09-08T10:00:00Z","now":"2026-09-08T10:11:00Z","ttl_seconds":600,"grace_seconds":300}`},
	}

	for _, test := range tests {
		t.Run(test.name+" anonymous", func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			result := httptest.NewRecorder()
			fixture.handler.ServeHTTP(result, request)
			if result.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d want=%d body=%s", result.Code, http.StatusUnauthorized, result.Body.String())
			}
		})
		t.Run(test.name+" viewer", func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.AddCookie(fixture.login(t, "viewer"))
			result := httptest.NewRecorder()
			fixture.handler.ServeHTTP(result, request)
			if result.Code != http.StatusForbidden {
				t.Fatalf("status=%d want=%d body=%s", result.Code, http.StatusForbidden, result.Body.String())
			}
		})
		t.Run(test.name+" operator", func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.AddCookie(fixture.login(t, "operator"))
			result := httptest.NewRecorder()
			fixture.handler.ServeHTTP(result, request)
			if result.Code != http.StatusOK {
				t.Fatalf("status=%d want=%d body=%s", result.Code, http.StatusOK, result.Body.String())
			}
		})
	}
}
