package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"control-center/internal/orchestration/change"
	"control-center/internal/orchestration/policy"
	productui "control-center/internal/ui"
)

func TestChangesJobsHandlerFailsClosedWithoutProvider(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/ui/changes-jobs", nil)
	w := httptest.NewRecorder()
	ChangesJobsHandler(nil).ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"state":"unavailable"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestChangesJobsHandlerHidesProviderErrors(t *testing.T) {
	provider := productui.ChangesJobsProviderFunc(func(context.Context) (productui.ChangesJobsView, error) {
		return productui.ChangesJobsView{}, errors.New("database password secret")
	})
	w := httptest.NewRecorder()
	ChangesJobsHandler(provider).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/ui/changes-jobs", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "database") {
		t.Fatalf("provider error leaked: %s", w.Body.String())
	}
}

func TestChangesJobsHandlerReturnsValidatedSnapshot(t *testing.T) {
	generatedAt := time.Date(2026, 9, 12, 2, 0, 0, 0, time.UTC)
	provider := productui.ChangesJobsProviderFunc(func(context.Context) (productui.ChangesJobsView, error) {
		return productui.ChangesJobsView{
			ContractVersion: productui.ChangesJobsContractVersion,
			State:           productui.ChangesJobsCurrent,
			GeneratedAt:     &generatedAt,
			ChangeCount:     1,
			JobCount:        0,
			Changes: []productui.ChangeOperationalView{{
				ID: "change-a", Action: "service.ensure", Requester: "operator-a", RevisionID: "revision-a",
				Risk: policy.RiskLow, State: change.StateApproved, PolicyEffect: policy.EffectAllow,
				Approvals: productui.ApprovalSummary{Satisfied: true}, Version: 1, UpdatedAt: generatedAt,
				SemanticDiff: productui.EvidenceUnavailable, BlastRadius: productui.EvidenceUnavailable,
				MaintenanceWindow: productui.EvidenceUnavailable, RecoveryEvidence: productui.EvidenceUnavailable,
				WorkflowEvidence: productui.WorkflowEvidenceSummary{Availability: productui.EvidenceUnavailable},
				Jobs:             []productui.JobOperationalView{},
			}},
		}, nil
	})
	w := httptest.NewRecorder()
	ChangesJobsHandler(provider).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/ui/changes-jobs", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"change_count":1`) || !strings.Contains(w.Body.String(), `"state":"current"`) || !strings.Contains(w.Body.String(), `"workflow_evidence":{"availability":"unavailable"`) {
		t.Fatalf("body=%s", w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q", got)
	}
}

func TestChangesJobsHandlerRejectsMutationMethods(t *testing.T) {
	provider := productui.ChangesJobsProviderFunc(func(context.Context) (productui.ChangesJobsView, error) {
		return productui.ChangesJobsView{}, nil
	})
	w := httptest.NewRecorder()
	ChangesJobsHandler(provider).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/ui/changes-jobs", strings.NewReader(`{}`)))
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("status=%d allow=%q", w.Code, w.Header().Get("Allow"))
	}
}
