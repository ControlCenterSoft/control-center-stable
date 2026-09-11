package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"control-center/internal/nodes"
)

func TestEnrollmentPlanHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/nodes/enrollment/plan", strings.NewReader(`{"nodeId":"node-1","displayName":"Node 1","osFamily":"linux","architecture":"amd64","capabilities":["automation","inventory"]}`))
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var plan nodes.EnrollmentPlan
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.NodeID != "node-1" || len(plan.Steps) != 4 {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestEnrollmentPlanHandlerRejectsUnknownField(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/nodes/enrollment/plan", strings.NewReader(`{"nodeId":"node-1","displayName":"Node 1","osFamily":"linux","architecture":"amd64","command":"unexpected"}`))
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestEnrollmentPlanHandlerRejectsGET(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/nodes/enrollment/plan", nil)
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("status=%d allow=%q", rec.Code, rec.Header().Get("Allow"))
	}
}
