package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"control-center/internal/corecontracts"
	"control-center/internal/nodelifecycle"
)

func TestNodeLifecycleReadAndPlanRBAC(t *testing.T) {
	current := lifecycleAPITestObject()
	projection, err := nodelifecycle.NewMemoryProjection([]nodelifecycle.NodeLifecycle{current})
	if err != nil {
		t.Fatal(err)
	}
	fixture := newResourceAuthFixtureWithProductOptions(t, withNodeLifecycleProjection(projection))

	readTests := []struct {
		name       string
		username   string
		wantStatus int
	}{
		{name: "anonymous", wantStatus: http.StatusUnauthorized},
		{name: "unbound", username: "unbound", wantStatus: http.StatusForbidden},
		{name: "viewer", username: "viewer", wantStatus: http.StatusOK},
		{name: "auditor", username: "auditor", wantStatus: http.StatusOK},
		{name: "operator", username: "operator", wantStatus: http.StatusOK},
		{name: "administrator", username: "admin", wantStatus: http.StatusOK},
	}
	for _, test := range readTests {
		t.Run("read "+test.name, func(t *testing.T) {
			result := lifecycleAPIRequest(t, fixture, test.username, http.MethodGet, "/api/v1/nodes/node-lifecycle-1/lifecycle", "")
			if result.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", result.Code, test.wantStatus, result.Body.String())
			}
		})
	}

	body := lifecycleAPIPlanBody(current)
	planTests := []struct {
		name       string
		username   string
		wantStatus int
	}{
		{name: "anonymous", wantStatus: http.StatusUnauthorized},
		{name: "unbound", username: "unbound", wantStatus: http.StatusForbidden},
		{name: "viewer", username: "viewer", wantStatus: http.StatusForbidden},
		{name: "auditor", username: "auditor", wantStatus: http.StatusForbidden},
		{name: "operator", username: "operator", wantStatus: http.StatusOK},
		{name: "administrator", username: "admin", wantStatus: http.StatusOK},
	}
	for _, test := range planTests {
		t.Run("plan "+test.name, func(t *testing.T) {
			result := lifecycleAPIRequest(t, fixture, test.username, http.MethodPost, "/api/v1/nodes/node-lifecycle-1/lifecycle/transitions/plan", body)
			if result.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", result.Code, test.wantStatus, result.Body.String())
			}
		})
	}
}

func TestNodeLifecycleIntegratedPlanRemainsReadOnly(t *testing.T) {
	current := lifecycleAPITestObject()
	projection, err := nodelifecycle.NewMemoryProjection([]nodelifecycle.NodeLifecycle{current})
	if err != nil {
		t.Fatal(err)
	}
	fixture := newResourceAuthFixtureWithProductOptions(t, withNodeLifecycleProjection(projection))

	result := lifecycleAPIRequest(t, fixture, "operator", http.MethodPost, "/api/v1/nodes/node-lifecycle-1/lifecycle/transitions/plan", lifecycleAPIPlanBody(current))
	if result.Code != http.StatusOK {
		t.Fatalf("plan status=%d body=%s", result.Code, result.Body.String())
	}
	var plan nodelifecycle.TransitionPlan
	if err := json.Unmarshal(result.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if !plan.Accepted || !plan.PlanOnly || plan.HostMutation || plan.StateMutation || !plan.RequiresAuditedChangeJob {
		t.Fatalf("unsafe integrated plan: %#v", plan)
	}

	read := lifecycleAPIRequest(t, fixture, "viewer", http.MethodGet, "/api/v1/nodes/node-lifecycle-1/lifecycle", "")
	if read.Code != http.StatusOK {
		t.Fatalf("read status=%d body=%s", read.Code, read.Body.String())
	}
	var after nodelifecycle.NodeLifecycle
	if err := json.Unmarshal(read.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if after.State != current.State || after.Generation != current.Generation || after.ResourceVersion != current.ResourceVersion {
		t.Fatalf("API plan mutated projection: before=%#v after=%#v", current, after)
	}
}

func TestNodeLifecycleDefaultProjectionDoesNotFabricateState(t *testing.T) {
	fixture := newResourceAuthFixture(t)
	result := lifecycleAPIRequest(t, fixture, "operator", http.MethodGet, "/api/v1/nodes/node-lifecycle-1/lifecycle", "")
	if result.Code != http.StatusNotFound {
		t.Fatalf("status=%d want=%d body=%s", result.Code, http.StatusNotFound, result.Body.String())
	}
}

func TestNodeLifecycleProductPathIsExact(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "/api/v1/nodes/node-1/lifecycle", want: true},
		{path: "/api/v1/nodes/node-1/lifecycle/transitions/plan", want: true},
		{path: "/api/v1/nodes/enrollment/plan", want: false},
		{path: "/api/v1/nodes//lifecycle", want: false},
		{path: "/api/v1/nodes/node-1/lifecycle/", want: false},
		{path: "/api/v1/nodes/node-1/lifecycle/transitions", want: false},
		{path: "/api/v1/nodes/node-1/lifecycle/transitions/plan/execute", want: false},
	}
	for _, test := range tests {
		if got := isNodeLifecycleProductPath(test.path); got != test.want {
			t.Fatalf("isNodeLifecycleProductPath(%q)=%t want=%t", test.path, got, test.want)
		}
	}
}

func lifecycleAPIRequest(t *testing.T, fixture resourceAuthFixture, username, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if username != "" {
		request.AddCookie(fixture.login(t, username))
	}
	result := httptest.NewRecorder()
	fixture.handler.ServeHTTP(result, request)
	return result
}

func lifecycleAPITestObject() nodelifecycle.NodeLifecycle {
	created := time.Date(2026, 9, 8, 1, 0, 0, 0, time.UTC)
	updated := created.Add(time.Hour)
	return nodelifecycle.NodeLifecycle{
		ObjectMetadata: corecontracts.ObjectMetadata{
			ObjectID:        "node-lifecycle-1",
			ScopeID:         "site-a-resources",
			OwnerScope:      "site-a",
			Generation:      4,
			ResourceVersion: "rv:node-lifecycle-1:4",
			CreatedAt:       created,
			UpdatedAt:       updated,
		},
		State:          nodelifecycle.StateReady,
		StateChangedAt: updated,
	}
}

func lifecycleAPIPlanBody(current nodelifecycle.NodeLifecycle) string {
	return `{"to":"draining","type":"desired","precondition":{"object_id":"` + current.ObjectID + `","resource_version":"` + current.ResourceVersion + `","generation":4},"evidence":{"passed_checks":["maintenance-preflight-passed","scheduling-disabled"]}}`
}
