package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"control-center/internal/pxe"
)

func TestPXEPlanHandlerWindows(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pxe/plan", strings.NewReader(`{"name":"windows-standard","osFamily":"windows","architecture":"amd64","unattended":true,"postInstallAutomation":true}`))
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var plan pxe.Plan
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 5 || plan.Steps[1].Action != "wimboot-start-winpe" {
		t.Fatalf("plan=%#v", plan)
	}
}

func TestPXEPlanHandlerRejectsWindowsArm64(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pxe/plan", strings.NewReader(`{"name":"bad","osFamily":"windows","architecture":"arm64"}`))
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestPXEPlanHandlerRejectsGET(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/pxe/plan", nil)
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("status=%d allow=%q", rec.Code, rec.Header().Get("Allow"))
	}
}
