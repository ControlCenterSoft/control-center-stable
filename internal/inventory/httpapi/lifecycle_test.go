package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReconcileHandlerSelectsLatestAndSources(t *testing.T) {
	body := `{"observations":[{"device_id":"dev-1","source":"agent","hostname":"old","seen_at":"2026-09-08T10:00:00Z"},{"device_id":"dev-1","source":"scan","hostname":"new","seen_at":"2026-09-08T11:00:00Z"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/inventory/reconcile", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	ReconcileHandler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	for _, want := range []string{`"count":1`, `"hostname":"new"`, `"sources":["agent","scan"]`} {
		if !strings.Contains(res.Body.String(), want) {
			t.Fatalf("body=%s missing %s", res.Body.String(), want)
		}
	}
}

func TestFreshnessHandlerClassifiesStale(t *testing.T) {
	body := `{"observed_at":"2026-09-08T10:00:00Z","now":"2026-09-08T10:10:00Z","stale_after_seconds":300,"expire_after_seconds":900}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/inventory/freshness", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	FreshnessHandler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"state":"stale"`) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestFreshnessHandlerRejectsInvalidThresholds(t *testing.T) {
	body := `{"observed_at":"2026-09-08T10:00:00Z","now":"2026-09-08T10:10:00Z","stale_after_seconds":900,"expire_after_seconds":300}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/inventory/freshness", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	FreshnessHandler().ServeHTTP(res, req)
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}
