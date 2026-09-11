package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func productRequest(t *testing.T, fixture resourceAuthFixture, username, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if username != "" {
		request.AddCookie(fixture.login(t, username))
	}
	result := httptest.NewRecorder()
	fixture.handler.ServeHTTP(result, request)
	return result
}

func TestProductPlanningRBAC(t *testing.T) {
	fixture := newResourceAuthFixture(t)
	nodeBody := `{"nodeId":"node-2","displayName":"Node 2","osFamily":"linux","architecture":"amd64","capabilities":["automation"]}`

	if result := productRequest(t, fixture, "", http.MethodPost, "/api/v1/nodes/enrollment/plan", nodeBody); result.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous node plan status=%d body=%s", result.Code, result.Body.String())
	}
	if result := productRequest(t, fixture, "viewer", http.MethodPost, "/api/v1/nodes/enrollment/plan", nodeBody); result.Code != http.StatusForbidden {
		t.Fatalf("viewer node plan status=%d body=%s", result.Code, result.Body.String())
	}
	if result := productRequest(t, fixture, "operator", http.MethodPost, "/api/v1/nodes/enrollment/plan", nodeBody); result.Code != http.StatusOK {
		t.Fatalf("operator node plan status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestProductMarketReadRBAC(t *testing.T) {
	fixture := newResourceAuthFixture(t)
	if result := productRequest(t, fixture, "unbound", http.MethodGet, "/api/v1/market/manifests", ""); result.Code != http.StatusForbidden {
		t.Fatalf("unbound market status=%d body=%s", result.Code, result.Body.String())
	}
	if result := productRequest(t, fixture, "viewer", http.MethodGet, "/api/v1/market/manifests", ""); result.Code != http.StatusOK {
		t.Fatalf("viewer market status=%d body=%s", result.Code, result.Body.String())
	}
	if result := productRequest(t, fixture, "unbound", http.MethodGet, "/api/v2/market/manifests", ""); result.Code != http.StatusForbidden {
		t.Fatalf("unbound market v2 status=%d body=%s", result.Code, result.Body.String())
	}
	if result := productRequest(t, fixture, "viewer", http.MethodGet, "/api/v2/market/manifests", ""); result.Code != http.StatusOK {
		t.Fatalf("viewer market v2 status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestOperatorCanUseAutomationAndPXEPlanning(t *testing.T) {
	fixture := newResourceAuthFixture(t)
	cases := []struct {
		path string
		body string
	}{
		{path: "/api/v1/automation/plan", body: `{"target":{"id":"node-1","platform":"linux"},"operation":"package.ensure","arguments":{"name":"example","state":"present"}}`},
		{path: "/api/v1/pxe/plan", body: `{"name":"linux-standard","osFamily":"linux","architecture":"amd64","unattended":true,"postInstallAutomation":true}`},
	}
	for _, test := range cases {
		result := productRequest(t, fixture, "operator", http.MethodPost, test.path, test.body)
		if result.Code != http.StatusOK {
			t.Fatalf("POST %s status=%d body=%s", test.path, result.Code, result.Body.String())
		}
	}
}

func TestAdministratorRetainsProductAccess(t *testing.T) {
	fixture := newResourceAuthFixture(t)
	result := productRequest(t, fixture, "admin", http.MethodGet, "/api/v1/market/manifests/directory-services", "")
	if result.Code != http.StatusOK {
		t.Fatalf("administrator market status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestAdministratorCanReadMarketManifestV2(t *testing.T) {
	fixture := newResourceAuthFixture(t)
	result := productRequest(t, fixture, "admin", http.MethodGet, "/api/v2/market/manifests/directory-services", "")
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"schema_version":"market.manifest/v2"`) {
		t.Fatalf("administrator market v2 status=%d body=%s", result.Code, result.Body.String())
	}
}
