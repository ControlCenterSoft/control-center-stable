package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"control-center/internal/inventory"
)

func TestStateHandlerIngestAndQuery(t *testing.T) {
	handler := StateHandler(inventory.NewMemoryRegistry())
	body := `{"observations":[{"device_id":"dev-1","source":"agent","hostname":"node1","seen_at":"2026-09-08T18:00:00Z"}]}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/inventory/observations", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("ingest status=%d body=%s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/inventory/devices/dev-1", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "dev-1") {
		t.Fatalf("get status=%d body=%s", w.Code, w.Body.String())
	}
}
