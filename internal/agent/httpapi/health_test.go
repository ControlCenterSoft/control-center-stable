package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHeartbeatHandlerClassifiesDelayed(t *testing.T) {
	body := `{"last_seen":"2026-09-08T10:00:00Z","now":"2026-09-08T10:10:00Z","delayed_after_seconds":300,"offline_after_seconds":900}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/heartbeat/evaluate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	HeartbeatHandler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"state":"delayed"`) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestLeaseHandlerClassifiesGrace(t *testing.T) {
	body := `{"node_id":"node-1","last_heartbeat":"2026-09-08T10:00:00Z","now":"2026-09-08T10:11:00Z","ttl_seconds":600,"grace_seconds":300}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/lease/evaluate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	LeaseHandler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"state":"grace"`) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestHeartbeatHandlerRejectsInvalidThresholds(t *testing.T) {
	body := `{"last_seen":"2026-09-08T10:00:00Z","now":"2026-09-08T10:10:00Z","delayed_after_seconds":900,"offline_after_seconds":300}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/heartbeat/evaluate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	HeartbeatHandler().ServeHTTP(res, req)
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}
