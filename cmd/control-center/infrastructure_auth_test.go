package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	productui "control-center/internal/ui"
)

func TestInfrastructureInventorySurfacesAreAbsentWithoutAuthoritativeProvider(t *testing.T) {
	fixture := newResourceAuthFixture(t)
	for _, path := range []string{"/api/v1/ui/infrastructure", "/infrastructure"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(fixture.login(t, "viewer"))
		result := httptest.NewRecorder()
		fixture.handler.ServeHTTP(result, request)
		if result.Code != http.StatusNotFound {
			t.Fatalf("GET %s status=%d want=%d body=%s", path, result.Code, http.StatusNotFound, result.Body.String())
		}
	}
}

func TestInfrastructureInventoryEndpointRequiresResourcesRead(t *testing.T) {
	fixture := newResourceAuthFixtureWithProductOptions(t, withInfrastructureInventoryProvider(currentInfrastructureProvider()))

	tests := []struct {
		name       string
		username   string
		wantStatus int
	}{
		{name: "anonymous", wantStatus: http.StatusUnauthorized},
		{name: "unbound", username: "unbound", wantStatus: http.StatusForbidden},
		{name: "site-scoped-viewer-cannot-read-global-aggregate", username: "siteviewer", wantStatus: http.StatusForbidden},
		{name: "viewer", username: "viewer", wantStatus: http.StatusOK},
		{name: "auditor", username: "auditor", wantStatus: http.StatusOK},
		{name: "operator", username: "operator", wantStatus: http.StatusOK},
		{name: "administrator", username: "admin", wantStatus: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/ui/infrastructure", nil)
			if test.username != "" {
				request.AddCookie(fixture.login(t, test.username))
			}
			result := httptest.NewRecorder()
			fixture.handler.ServeHTTP(result, request)
			if result.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", result.Code, test.wantStatus, result.Body.String())
			}
		})
	}
}

func TestInfrastructureInventoryPageUsesSameReadPermissionAndSecurityHeaders(t *testing.T) {
	fixture := newResourceAuthFixtureWithProductOptions(t, withInfrastructureInventoryProvider(currentInfrastructureProvider()))

	anonymous := httptest.NewRecorder()
	fixture.handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/infrastructure", nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d body=%s", anonymous.Code, anonymous.Body.String())
	}

	siteScoped := httptest.NewRecorder()
	siteScopedRequest := httptest.NewRequest(http.MethodGet, "/infrastructure", nil)
	siteScopedRequest.AddCookie(fixture.login(t, "siteviewer"))
	fixture.handler.ServeHTTP(siteScoped, siteScopedRequest)
	if siteScoped.Code != http.StatusForbidden {
		t.Fatalf("site-scoped viewer status=%d want=%d body=%s", siteScoped.Code, http.StatusForbidden, siteScoped.Body.String())
	}

	request := httptest.NewRequest(http.MethodGet, "/infrastructure", nil)
	request.AddCookie(fixture.login(t, "viewer"))
	result := httptest.NewRecorder()
	fixture.handler.ServeHTTP(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	for header, want := range map[string]string{
		"Cache-Control":          "no-store",
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := result.Header().Get(header); got != want {
			t.Fatalf("%s=%q want=%q", header, got, want)
		}
	}
	body := result.Body.String()
	for _, fragment := range []string{"Сайты и узлы", "Alpha", "Пользователь: Viewer"} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("page missing %q: %s", fragment, body)
		}
	}
}

func TestInfrastructureInventoryPageKeepsUnavailableEvidenceFailClosed(t *testing.T) {
	provider := productui.InfrastructureInventoryProviderFunc(func(context.Context) (productui.InfrastructureInventory, error) {
		return productui.InfrastructureInventory{
			ContractVersion: productui.InfrastructureInventoryContractVersion,
			State:           productui.InventoryViewUnavailable,
			Sites:           []productui.SiteInventory{},
		}, nil
	})
	fixture := newResourceAuthFixtureWithProductOptions(t, withInfrastructureInventoryProvider(provider))
	request := httptest.NewRequest(http.MethodGet, "/infrastructure", nil)
	request.AddCookie(fixture.login(t, "viewer"))
	result := httptest.NewRecorder()
	fixture.handler.ServeHTTP(result, request)
	if result.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	if !strings.Contains(result.Body.String(), "Инвентарь недоступен") || strings.Contains(result.Body.String(), "Сайтов: 0") {
		t.Fatalf("unavailable evidence rendered unsafely: %s", result.Body.String())
	}
}

func currentInfrastructureProvider() productui.InfrastructureInventoryProvider {
	generatedAt := time.Date(2026, 9, 12, 1, 0, 0, 0, time.UTC)
	return productui.InfrastructureInventoryProviderFunc(func(context.Context) (productui.InfrastructureInventory, error) {
		return productui.InfrastructureInventory{
			ContractVersion: productui.InfrastructureInventoryContractVersion,
			State:           productui.InventoryViewCurrent,
			GeneratedAt:     &generatedAt,
			SiteCount:       1,
			NodeCount:       0,
			Sites: []productui.SiteInventory{{
				ID: "site-a", Name: "Alpha", ScopeID: "scope-a", NodeCount: 0, Nodes: []productui.NodeInventory{},
			}},
		}, nil
	})
}
