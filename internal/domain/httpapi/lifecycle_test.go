package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"control-center/internal/domain"
)

func TestLifecyclePlanHandlerBuildsTypedDeterministicPlan(t *testing.T) {
	body := `{"provider":"freeipa","operation":"promote","domain_name":"Example.TEST.","target":{"node_id":"ipa-02","platform":"linux"},"requirements":{}}`
	first := lifecyclePlanRequest(t, http.MethodPost, body)
	second := lifecyclePlanRequest(t, http.MethodPost, body)
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("statuses = %d, %d; bodies = %s, %s", first.Code, second.Code, first.Body.String(), second.Body.String())
	}

	var firstPlan, secondPlan domain.LifecyclePlan
	if err := json.Unmarshal(first.Body.Bytes(), &firstPlan); err != nil {
		t.Fatalf("decode first plan: %v", err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondPlan); err != nil {
		t.Fatalf("decode second plan: %v", err)
	}
	if firstPlan.PlanID == "" || firstPlan.PlanID != secondPlan.PlanID {
		t.Fatalf("plan ids = %q, %q", firstPlan.PlanID, secondPlan.PlanID)
	}
	if firstPlan.Provider != domain.ProviderFreeIPA || firstPlan.DomainName != "example.test" {
		t.Fatalf("plan = %#v", firstPlan)
	}
	if !containsAction(firstPlan.Steps, domain.ActionFreeIPAPromoteReplica) {
		t.Fatalf("steps = %#v", firstPlan.Steps)
	}
	for _, forbidden := range []string{"password", "credential", "command", "endpoint"} {
		if strings.Contains(strings.ToLower(first.Body.String()), forbidden) {
			t.Fatalf("response exposes forbidden field %q: %s", forbidden, first.Body.String())
		}
	}
}

func TestLifecyclePlanHandlerHealthPreflightIsReadOnly(t *testing.T) {
	result := lifecyclePlanRequest(t, http.MethodPost, `{"provider":"samba-ad-dc","operation":"health-preflight","domain_name":"example.test","target":{"node_id":"dc-01","platform":"linux"}}`)
	if result.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	var plan domain.LifecyclePlan
	if err := json.Unmarshal(result.Body.Bytes(), &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	if plan.Mutating {
		t.Fatalf("health preflight is mutating: %#v", plan)
	}
	for _, step := range plan.Steps {
		if step.Stage != domain.LifecycleStagePreflight {
			t.Fatalf("health preflight contains stage %q", step.Stage)
		}
	}
}

func TestLifecyclePlanHandlerRejectsUnstructuredOrSecretInputs(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "credential payload", body: `{"provider":"samba-ad-dc","operation":"create","domain_name":"example.test","target":{"node_id":"dc-01","platform":"linux"},"password":"secret"}`},
		{name: "arbitrary command", body: `{"provider":"samba-ad-dc","operation":"create","domain_name":"example.test","target":{"node_id":"dc-01","platform":"linux"},"command":"sh -c anything"}`},
		{name: "environment endpoint", body: `{"provider":"samba-ad-dc","operation":"create","domain_name":"example.test","target":{"node_id":"dc-01","platform":"linux"},"endpoint":"https://internal.example"}`},
		{name: "unknown target field", body: `{"provider":"samba-ad-dc","operation":"create","domain_name":"example.test","target":{"node_id":"dc-01","platform":"linux","address":"192.0.2.10"}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := lifecyclePlanRequest(t, http.MethodPost, test.body)
			if result.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
			}
		})
	}
}

func TestLifecyclePlanHandlerRejectsImplicitOrIncompatibleProvider(t *testing.T) {
	tests := []string{
		`{"provider":"auto","operation":"create","domain_name":"example.test","target":{"node_id":"dc-01","platform":"linux"}}`,
		`{"provider":"freeipa","operation":"join","domain_name":"example.test","target":{"node_id":"win-01","platform":"windows"}}`,
		`{"provider":"freeipa","operation":"create","domain_name":"example.test","target":{"node_id":"ipa-01","platform":"linux"},"requirements":{"GroupPolicy":true}}`,
	}
	for _, body := range tests {
		result := lifecyclePlanRequest(t, http.MethodPost, body)
		if result.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status=%d body=%s request=%s", result.Code, result.Body.String(), body)
		}
	}
}

func TestLifecyclePlanHandlerBoundary(t *testing.T) {
	t.Run("method", func(t *testing.T) {
		result := lifecyclePlanRequest(t, http.MethodGet, "")
		if result.Code != http.StatusMethodNotAllowed || result.Header().Get("Allow") != http.MethodPost {
			t.Fatalf("status=%d allow=%q", result.Code, result.Header().Get("Allow"))
		}
	})
	t.Run("content type", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/domain/lifecycle/plan", strings.NewReader(`{}`))
		result := httptest.NewRecorder()
		LifecyclePlanHandler().ServeHTTP(result, request)
		if result.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
		}
	})
	t.Run("trailing json", func(t *testing.T) {
		result := lifecyclePlanRequest(t, http.MethodPost, `{} {}`)
		if result.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
		}
	})
}

func lifecyclePlanRequest(t *testing.T, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "/api/v1/domain/lifecycle/plan", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	result := httptest.NewRecorder()
	LifecyclePlanHandler().ServeHTTP(result, request)
	return result
}

func containsAction(steps []domain.LifecycleStep, action domain.LifecycleAction) bool {
	for _, step := range steps {
		if step.Action == action {
			return true
		}
	}
	return false
}
