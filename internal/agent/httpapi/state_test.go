package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"control-center/internal/agent"
)

func TestStateHandlerEnrollmentHeartbeatAndQuery(t *testing.T) {
	handler := StateHandler(agent.NewMemoryRegistry())
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agent/enrollments", strings.NewReader(`{"node_id":"node-1","hostname":"host1","capabilities":["inventory"]}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("enroll status=%d body=%s", w.Code, w.Body.String())
	}
	r = httptest.NewRequest(http.MethodPost, "/api/v1/agent/heartbeats", strings.NewReader(`{"node_id":"node-1","seen_at":"2026-09-08T18:00:00Z"}`))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("heartbeat status=%d body=%s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/agent/nodes/node-1", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "node-1") {
		t.Fatalf("get status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestStateHandlerRejectsUnpersistedOrTrailingFields(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
	}{
		{
			name: "v2 enrollment field",
			path: "/api/v1/agent/enrollments",
			body: `{"node_id":"node-1","hostname":"host1","roles":["worker"]}`,
		},
		{
			name: "enrollment trailing value",
			path: "/api/v1/agent/enrollments",
			body: `{"node_id":"node-1","hostname":"host1"} {}`,
		},
		{
			name: "heartbeat unknown field",
			path: "/api/v1/agent/heartbeats",
			body: `{"node_id":"node-1","seen_at":"2026-09-08T18:00:00Z","ignored":true}`,
		},
		{
			name: "heartbeat trailing value",
			path: "/api/v1/agent/heartbeats",
			body: `{"node_id":"node-1","seen_at":"2026-09-08T18:00:00Z"} {}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := StateHandler(agent.NewMemoryRegistry())
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
