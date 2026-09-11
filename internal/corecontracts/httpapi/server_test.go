package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"control-center/internal/corecontracts"
)

func TestDistributedCoreReadAPI(t *testing.T) {
	repository := testRepository(t)
	guardCalls := 0
	guard := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			guardCalls++
			next.ServeHTTP(w, r)
		})
	}
	handler := New(testLogger(), repository, guard).Handler()

	result := request(handler, http.MethodGet, "/api/v1/core/objects?object_type=scope")
	if result.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", result.Code, result.Body.String())
	}
	var list struct {
		Items []corecontracts.StoredObject `json:"items"`
		Count int                          `json:"count"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Count != 1 || list.Items[0].ObjectID != "global" {
		t.Fatalf("list = %#v", list)
	}

	result = request(handler, http.MethodGet, "/api/v1/core/objects/desired-a")
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"resource_version"`) {
		t.Fatalf("get status=%d body=%s", result.Code, result.Body.String())
	}
	result = request(handler, http.MethodGet, "/api/v1/core/topology")
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"scopes":[`) ||
		!strings.Contains(result.Body.String(), `"management_zones":[]`) ||
		!strings.Contains(result.Body.String(), `"network_zones":[]`) ||
		!strings.Contains(result.Body.String(), `"network_interfaces":[]`) {
		t.Fatalf("topology status=%d body=%s", result.Code, result.Body.String())
	}
	if guardCalls != 3 {
		t.Fatalf("guard calls=%d, want 3", guardCalls)
	}
}

func TestDistributedCoreReadAPIIncludesNetworkSnapshot(t *testing.T) {
	repository := testRepository(t)
	requests := []corecontracts.MutationRequest{
		{
			Operation: corecontracts.MutationCreate, ObjectType: corecontracts.ObjectScope,
			ObjectID: "scope-site-a", ScopeID: "global", OwnerScope: "global",
			Document: json.RawMessage(`{"id":"scope-site-a","kind":"site","name":"Site A scope","parent_id":"global","delegated_authorities":["configuration","desired-state"]}`),
		},
		{
			Operation: corecontracts.MutationCreate, ObjectType: corecontracts.ObjectSite,
			ObjectID: "site-a", ScopeID: "scope-site-a", OwnerScope: "global",
			Document: json.RawMessage(`{"id":"site-a","name":"Site A","scope_id":"scope-site-a"}`),
		},
		{
			Operation: corecontracts.MutationCreate, ObjectType: corecontracts.ObjectRoleAssignment,
			ObjectID: "role-agent-a", ScopeID: "scope-site-a", OwnerScope: "global",
			Document: json.RawMessage(`{"target_node_id":"node-a","service_identity_id":"agent-a","role":"agent","site_id":"site-a"}`),
		},
		{
			Operation: corecontracts.MutationCreate, ObjectType: corecontracts.ObjectNetworkZone,
			ObjectID: "network-zone-a", ScopeID: "scope-site-a", OwnerScope: "global",
			Document: json.RawMessage(`{"id":"network-zone-a","name":"Site A LAN","kind":"lan","scope_id":"scope-site-a","site_id":"site-a"}`),
		},
		{
			Operation: corecontracts.MutationCreate, ObjectType: corecontracts.ObjectNetworkInterface,
			ObjectID: "network-interface-a", ScopeID: "scope-site-a", OwnerScope: "global",
			Document: json.RawMessage(`{"id":"network-interface-a","node_id":"node-a","name":"eth0","kind":"physical","scope_id":"scope-site-a","site_id":"site-a","network_zone_id":"network-zone-a","mac_address":"02:00:00:00:00:01","operational_state":"up","mtu":1500}`),
		},
	}
	for index, mutation := range requests {
		if _, err := repository.Apply(context.Background(), mutation, fmt.Sprintf("network-read-api-%d", index)); err != nil {
			t.Fatalf("persist %s: %v", mutation.ObjectType, err)
		}
	}
	handler := New(testLogger(), repository, func(next http.Handler) http.Handler { return next }).Handler()

	result := request(handler, http.MethodGet, "/api/v1/core/objects?object_type=network-interface")
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"object_id":"network-interface-a"`) || !strings.Contains(result.Body.String(), `"count":1`) {
		t.Fatalf("network interface list status=%d body=%s", result.Code, result.Body.String())
	}
	result = request(handler, http.MethodGet, "/api/v1/core/topology")
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"network_zones":[{"object_id":"network-zone-a"`) || !strings.Contains(result.Body.String(), `"network_interfaces":[{"object_id":"network-interface-a"`) {
		t.Fatalf("network topology status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestDistributedCoreReadAPIRejectsInvalidRequests(t *testing.T) {
	handler := New(testLogger(), testRepository(t), func(next http.Handler) http.Handler { return next }).Handler()
	tests := []struct {
		method string
		path   string
		status int
	}{
		{http.MethodPost, "/api/v1/core/objects", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/v1/core/objects?unexpected=true", http.StatusBadRequest},
		{http.MethodGet, "/api/v1/core/objects?object_type=secret", http.StatusBadRequest},
		{http.MethodGet, "/api/v1/core/objects?scope_id=bad%20scope", http.StatusBadRequest},
		{http.MethodGet, "/api/v1/core/objects/bad%20id", http.StatusBadRequest},
		{http.MethodGet, "/api/v1/core/objects/missing", http.StatusNotFound},
		{http.MethodGet, "/api/v1/core/topology?unexpected=true", http.StatusBadRequest},
	}
	for _, test := range tests {
		result := request(handler, test.method, test.path)
		if result.Code != test.status {
			t.Errorf("%s %s status=%d want=%d body=%s", test.method, test.path, result.Code, test.status, result.Body.String())
		}
	}
}

func TestDistributedCoreReadAPIFailsClosed(t *testing.T) {
	anonymous := New(testLogger(), testRepository(t), nil).Handler()
	if result := request(anonymous, http.MethodGet, "/api/v1/core/objects"); result.Code != http.StatusUnauthorized {
		t.Fatalf("missing guard status=%d body=%s", result.Code, result.Body.String())
	}
	unready := New(testLogger(), nil, func(next http.Handler) http.Handler { return next }).Handler()
	if result := request(unready, http.MethodGet, "/api/v1/core/objects"); result.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing repository status=%d body=%s", result.Code, result.Body.String())
	}
}

func testRepository(t *testing.T) *corecontracts.MemoryObjectRepository {
	t.Helper()
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	repository, err := corecontracts.NewMemoryObjectRepository([]corecontracts.StoredObject{corecontracts.LegacyGlobalScopeObject(now)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.Apply(context.Background(), corecontracts.MutationRequest{
		Operation: corecontracts.MutationCreate, ObjectType: corecontracts.ObjectDesiredState,
		ObjectID: "desired-a", ScopeID: "global", OwnerScope: "global",
		Document: json.RawMessage(`{"kind":"service.config","target_object_id":"service-a","spec":{}}`),
	}, "read-api-fixture")
	if err != nil {
		t.Fatal(err)
	}
	return repository
}

func request(handler http.Handler, method, path string) *httptest.ResponseRecorder {
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, httptest.NewRequest(method, path, nil))
	return result
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
