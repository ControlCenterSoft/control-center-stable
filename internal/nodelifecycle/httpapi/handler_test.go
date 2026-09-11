package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"control-center/internal/corecontracts"
	"control-center/internal/nodelifecycle"
)

func TestHandlerReadsImmutableProjection(t *testing.T) {
	current := testLifecycle(nodelifecycle.StateReady)
	handler := testHandler(t, current)

	result := lifecycleRequest(handler, http.MethodGet, "/api/v1/nodes/node-1/lifecycle", "", "")
	if result.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", result.Code, result.Body.String())
	}
	var got nodelifecycle.NodeLifecycle
	if err := json.Unmarshal(result.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode lifecycle: %v", err)
	}
	if got.ObjectID != current.ObjectID || got.State != current.State || got.ResourceVersion != current.ResourceVersion {
		t.Fatalf("GET lifecycle = %#v", got)
	}

	missing := lifecycleRequest(handler, http.MethodGet, "/api/v1/nodes/missing/lifecycle", "", "")
	if missing.Code != http.StatusNotFound || errorCode(t, missing) != "node_lifecycle_not_found" {
		t.Fatalf("missing status=%d body=%s", missing.Code, missing.Body.String())
	}

	unavailable := New(nil)
	result = lifecycleRequest(unavailable, http.MethodGet, "/api/v1/nodes/node-1/lifecycle", "", "")
	if result.Code != http.StatusServiceUnavailable || errorCode(t, result) != "lifecycle_projection_unavailable" {
		t.Fatalf("unavailable status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestHandlerFailsClosedForInvalidOrMismatchedProjection(t *testing.T) {
	current := testLifecycle(nodelifecycle.StateReady)
	tests := []struct {
		name       string
		projection nodelifecycle.Projection
	}{
		{
			name: "invalid object",
			projection: projectionFunc(func(context.Context, string) (nodelifecycle.NodeLifecycle, error) {
				invalid := current
				invalid.ResourceVersion = ""
				return invalid, nil
			}),
		},
		{
			name: "mismatched object",
			projection: projectionFunc(func(context.Context, string) (nodelifecycle.NodeLifecycle, error) {
				other := current
				other.ObjectID = "node-2"
				other.ResourceVersion = "rv:node-2:7"
				return other, nil
			}),
		},
		{
			name: "adapter failure",
			projection: projectionFunc(func(context.Context, string) (nodelifecycle.NodeLifecycle, error) {
				return nodelifecycle.NodeLifecycle{}, errors.New("backend details must not escape")
			}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := New(test.projection)
			for _, request := range []struct {
				method string
				path   string
				body   string
				media  string
			}{
				{method: http.MethodGet, path: "/api/v1/nodes/node-1/lifecycle"},
				{method: http.MethodPost, path: "/api/v1/nodes/node-1/lifecycle/transitions/plan", body: validDrainPlanBody(current), media: "application/json"},
			} {
				result := lifecycleRequest(handler, request.method, request.path, request.body, request.media)
				if result.Code != http.StatusServiceUnavailable || errorCode(t, result) != "lifecycle_projection_unavailable" {
					t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
				}
				if strings.Contains(result.Body.String(), "backend details") {
					t.Fatalf("response leaked adapter error: %s", result.Body.String())
				}
			}
		})
	}
}

func TestHandlerPlansTransitionWithoutStateOrHostMutation(t *testing.T) {
	current := testLifecycle(nodelifecycle.StateReady)
	handler := testHandler(t, current)
	body := validDrainPlanBody(current)

	first := lifecycleRequest(handler, http.MethodPost, "/api/v1/nodes/node-1/lifecycle/transitions/plan", body, "application/json; charset=UTF-8")
	second := lifecycleRequest(handler, http.MethodPost, "/api/v1/nodes/node-1/lifecycle/transitions/plan", body, "application/json")
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("plan statuses=%d,%d bodies=%s %s", first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	var firstPlan, secondPlan nodelifecycle.TransitionPlan
	if err := json.Unmarshal(first.Body.Bytes(), &firstPlan); err != nil {
		t.Fatalf("decode first plan: %v", err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondPlan); err != nil {
		t.Fatalf("decode second plan: %v", err)
	}
	if firstPlan.PlanID == "" || firstPlan.PlanID != secondPlan.PlanID {
		t.Fatalf("plan IDs = %q, %q", firstPlan.PlanID, secondPlan.PlanID)
	}
	if !firstPlan.Accepted || !firstPlan.PlanOnly || firstPlan.HostMutation || firstPlan.StateMutation || !firstPlan.RequiresAuditedChangeJob {
		t.Fatalf("unsafe plan response: %#v", firstPlan)
	}
	if firstPlan.From != nodelifecycle.StateReady || firstPlan.To != nodelifecycle.StateDraining || firstPlan.PlannedGeneration != current.Generation+1 {
		t.Fatalf("unexpected plan: %#v", firstPlan)
	}
	for _, forbidden := range []string{"password", "credential", "shell", "command", "next_resource_version"} {
		if strings.Contains(strings.ToLower(first.Body.String()), forbidden) {
			t.Fatalf("plan response contains forbidden field %q: %s", forbidden, first.Body.String())
		}
	}

	projectionResult := lifecycleRequest(handler, http.MethodGet, "/api/v1/nodes/node-1/lifecycle", "", "")
	if projectionResult.Code != http.StatusOK {
		t.Fatalf("projection status=%d body=%s", projectionResult.Code, projectionResult.Body.String())
	}
	var after nodelifecycle.NodeLifecycle
	if err := json.Unmarshal(projectionResult.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if after.State != current.State || after.Generation != current.Generation || after.ResourceVersion != current.ResourceVersion {
		t.Fatalf("planning mutated projection: before=%#v after=%#v", current, after)
	}
}

func TestHandlerRejectsMethodsQueriesAndUnsupportedMedia(t *testing.T) {
	current := testLifecycle(nodelifecycle.StateReady)
	handler := testHandler(t, current)
	body := validDrainPlanBody(current)

	tests := []struct {
		name        string
		method      string
		path        string
		body        string
		contentType string
		addHeader   string
		want        int
		allow       string
	}{
		{name: "get plan", method: http.MethodGet, path: "/api/v1/nodes/node-1/lifecycle/transitions/plan", want: http.StatusMethodNotAllowed, allow: http.MethodPost},
		{name: "post read", method: http.MethodPost, path: "/api/v1/nodes/node-1/lifecycle", body: body, contentType: "application/json", want: http.StatusMethodNotAllowed, allow: http.MethodGet},
		{name: "read query", method: http.MethodGet, path: "/api/v1/nodes/node-1/lifecycle?expand=all", want: http.StatusBadRequest},
		{name: "plan query", method: http.MethodPost, path: "/api/v1/nodes/node-1/lifecycle/transitions/plan?execute=true", body: body, contentType: "application/json", want: http.StatusBadRequest},
		{name: "missing content type", method: http.MethodPost, path: "/api/v1/nodes/node-1/lifecycle/transitions/plan", body: body, want: http.StatusUnsupportedMediaType},
		{name: "plain text", method: http.MethodPost, path: "/api/v1/nodes/node-1/lifecycle/transitions/plan", body: body, contentType: "text/plain", want: http.StatusUnsupportedMediaType},
		{name: "json suffix", method: http.MethodPost, path: "/api/v1/nodes/node-1/lifecycle/transitions/plan", body: body, contentType: "application/json-patch+json", want: http.StatusUnsupportedMediaType},
		{name: "json prefix", method: http.MethodPost, path: "/api/v1/nodes/node-1/lifecycle/transitions/plan", body: body, contentType: "application/json-invalid", want: http.StatusUnsupportedMediaType},
		{name: "wrong charset", method: http.MethodPost, path: "/api/v1/nodes/node-1/lifecycle/transitions/plan", body: body, contentType: "application/json; charset=latin1", want: http.StatusUnsupportedMediaType},
		{name: "unsupported media parameter", method: http.MethodPost, path: "/api/v1/nodes/node-1/lifecycle/transitions/plan", body: body, contentType: "application/json; profile=test", want: http.StatusUnsupportedMediaType},
		{name: "duplicate content type", method: http.MethodPost, path: "/api/v1/nodes/node-1/lifecycle/transitions/plan", body: body, contentType: "application/json", addHeader: "application/json", want: http.StatusUnsupportedMediaType},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			if test.addHeader != "" {
				request.Header.Add("Content-Type", test.addHeader)
			}
			result := httptest.NewRecorder()
			handler.ServeHTTP(result, request)
			if result.Code != test.want || result.Header().Get("Allow") != test.allow {
				t.Fatalf("status=%d want=%d allow=%q want=%q body=%s", result.Code, test.want, result.Header().Get("Allow"), test.allow, result.Body.String())
			}
		})
	}
}

func TestHandlerRejectsMalformedUnknownDuplicateAndTrailingJSON(t *testing.T) {
	current := testLifecycle(nodelifecycle.StateReady)
	handler := testHandler(t, current)
	valid := validDrainPlanBody(current)
	tests := []struct {
		name string
		body string
	}{
		{name: "empty", body: ""},
		{name: "malformed", body: "{"},
		{name: "null root", body: "null"},
		{name: "array root", body: "[]"},
		{name: "scalar root", body: `"transition"`},
		{name: "missing required fields", body: `{}`},
		{name: "unknown top level", body: strings.TrimSuffix(valid, "}") + `,"execute":true}`},
		{name: "case variant field", body: strings.Replace(valid, `"to"`, `"To"`, 1)},
		{name: "duplicate target", body: strings.Replace(valid, `"to":"draining"`, `"to":"draining","to":"offline"`, 1)},
		{name: "escaped duplicate target", body: strings.Replace(valid, `"to":"draining"`, `"to":"draining","\u0074o":"offline"`, 1)},
		{name: "unknown precondition field", body: strings.Replace(valid, `"generation":7`, `"generation":7,"revision":7`, 1)},
		{name: "duplicate precondition field", body: strings.Replace(valid, `"generation":7`, `"generation":7,"generation":8`, 1)},
		{name: "unknown evidence field", body: strings.Replace(valid, `"passed_checks":`, `"source":"client","passed_checks":`, 1)},
		{name: "duplicate evidence field", body: strings.Replace(valid, `"passed_checks":`, `"passed_checks":[],"passed_checks":`, 1)},
		{name: "null evidence", body: strings.Replace(valid, `"evidence":{"passed_checks":["maintenance-preflight-passed","scheduling-disabled"]}`, `"evidence":null`, 1)},
		{name: "null checks", body: strings.Replace(valid, `["maintenance-preflight-passed","scheduling-disabled"]`, `null`, 1)},
		{name: "trailing object", body: valid + `{}`},
		{name: "trailing scalar", body: valid + ` true`},
		{name: "secret field", body: strings.TrimSuffix(valid, "}") + `,"password":"redacted"}`},
		{name: "command field", body: strings.TrimSuffix(valid, "}") + `,"command":"redacted"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := lifecycleRequest(handler, http.MethodPost, "/api/v1/nodes/node-1/lifecycle/transitions/plan", test.body, "application/json")
			if result.Code != http.StatusBadRequest || errorCode(t, result) != "invalid_request" {
				t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
			}
		})
	}

	oversized := `{"padding":"` + strings.Repeat("x", int(maxTransitionPlanRequestBytes)) + `"}`
	result := lifecycleRequest(handler, http.MethodPost, "/api/v1/nodes/node-1/lifecycle/transitions/plan", oversized, "application/json")
	if result.Code != http.StatusRequestEntityTooLarge || errorCode(t, result) != "request_too_large" {
		t.Fatalf("oversized status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestHandlerClassifiesContractAndProjectionFailures(t *testing.T) {
	current := testLifecycle(nodelifecycle.StateReady)
	handler := testHandler(t, current)
	valid := validDrainPlanBody(current)
	tests := []struct {
		name string
		body string
		want int
		code string
	}{
		{name: "stale version", body: strings.Replace(valid, current.ResourceVersion, "rv:stale", 1), want: http.StatusConflict, code: "precondition_failed"},
		{name: "direct retirement", body: `{"to":"retired","type":"desired","reason":"unsafe shortcut","precondition":{"object_id":"node-1","resource_version":"rv:node-1:7","generation":7},"evidence":{"passed_checks":["retirement-approved","no-managed-state"]}}`, want: http.StatusConflict, code: "invalid_transition"},
		{name: "missing evidence", body: strings.Replace(valid, `["maintenance-preflight-passed","scheduling-disabled"]`, `[]`, 1), want: http.StatusUnprocessableEntity, code: "invalid_transition_evidence"},
		{name: "unknown evidence", body: strings.Replace(valid, `"scheduling-disabled"`, `"filesystem-copied"`, 1), want: http.StatusUnprocessableEntity, code: "invalid_transition_evidence"},
		{name: "missing resource version", body: strings.Replace(valid, `"resource_version":"rv:node-1:7",`, "", 1), want: http.StatusUnprocessableEntity, code: "invalid_precondition"},
		{name: "bad incident reason", body: `{"to":"offline","type":"observation","precondition":{"object_id":"node-1","resource_version":"rv:node-1:7","generation":7},"evidence":{"passed_checks":[]}}`, want: http.StatusUnprocessableEntity, code: "invalid_node_lifecycle"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := lifecycleRequest(handler, http.MethodPost, "/api/v1/nodes/node-1/lifecycle/transitions/plan", test.body, "application/json")
			if result.Code != test.want || errorCode(t, result) != test.code {
				t.Fatalf("status=%d want=%d code=%q want=%q body=%s", result.Code, test.want, errorCode(t, result), test.code, result.Body.String())
			}
		})
	}

	missing := lifecycleRequest(handler, http.MethodPost, "/api/v1/nodes/missing/lifecycle/transitions/plan", valid, "application/json")
	if missing.Code != http.StatusNotFound || errorCode(t, missing) != "node_lifecycle_not_found" {
		t.Fatalf("missing status=%d body=%s", missing.Code, missing.Body.String())
	}

	clockBehind := New(mustProjection(t, current), WithClock(func() time.Time { return current.UpdatedAt }))
	result := lifecycleRequest(clockBehind, http.MethodPost, "/api/v1/nodes/node-1/lifecycle/transitions/plan", valid, "application/json")
	if result.Code != http.StatusServiceUnavailable || errorCode(t, result) != "lifecycle_projection_unavailable" {
		t.Fatalf("clock status=%d body=%s", result.Code, result.Body.String())
	}
}

func testHandler(t *testing.T, lifecycles ...nodelifecycle.NodeLifecycle) http.Handler {
	t.Helper()
	projection := mustProjection(t, lifecycles...)
	return New(projection, WithClock(func() time.Time {
		return time.Date(2026, 9, 8, 14, 0, 0, 0, time.UTC)
	}))
}

func mustProjection(t *testing.T, lifecycles ...nodelifecycle.NodeLifecycle) *nodelifecycle.MemoryProjection {
	t.Helper()
	projection, err := nodelifecycle.NewMemoryProjection(lifecycles)
	if err != nil {
		t.Fatalf("NewMemoryProjection() error = %v", err)
	}
	return projection
}

func testLifecycle(state nodelifecycle.State) nodelifecycle.NodeLifecycle {
	created := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	updated := created.Add(time.Hour)
	reason := ""
	if state == nodelifecycle.StateOffline {
		reason = "heartbeat lease expired"
	}
	return nodelifecycle.NodeLifecycle{
		ObjectMetadata: corecontracts.ObjectMetadata{
			ObjectID:        "node-1",
			ScopeID:         "site-a-resources",
			OwnerScope:      "site-a",
			Generation:      7,
			ResourceVersion: "rv:node-1:7",
			CreatedAt:       created,
			UpdatedAt:       updated,
		},
		State:          state,
		StateChangedAt: updated,
		Reason:         reason,
	}
}

func validDrainPlanBody(current nodelifecycle.NodeLifecycle) string {
	return `{"to":"draining","type":"desired","precondition":{"object_id":"` + current.ObjectID + `","resource_version":"` + current.ResourceVersion + `","generation":7},"evidence":{"passed_checks":["maintenance-preflight-passed","scheduling-disabled"]}}`
}

func lifecycleRequest(handler http.Handler, method, path, body, contentType string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	return result
}

func errorCode(t *testing.T, result *httptest.ResponseRecorder) string {
	t.Helper()
	var response errorResponse
	if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v body=%s", err, result.Body.String())
	}
	return response.Error.Code
}

type projectionFunc func(context.Context, string) (nodelifecycle.NodeLifecycle, error)

func (f projectionFunc) Get(ctx context.Context, nodeID string) (nodelifecycle.NodeLifecycle, error) {
	return f(ctx, nodeID)
}
