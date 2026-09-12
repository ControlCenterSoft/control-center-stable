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

func TestChangesJobsSurfaceIsAbsentWithoutAuthoritativeProvider(t *testing.T) {
	fixture := newResourceAuthFixture(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/ui/changes-jobs", nil)
	request.AddCookie(fixture.login(t, "operator"))
	result := httptest.NewRecorder()
	fixture.handler.ServeHTTP(result, request)
	if result.Code != http.StatusNotFound {
		t.Fatalf("status=%d want=%d body=%s", result.Code, http.StatusNotFound, result.Body.String())
	}
}

func TestChangesJobsEndpointRequiresGlobalJobsRead(t *testing.T) {
	fixture := newResourceAuthFixtureWithProductOptions(t, withChangesJobsProvider(currentChangesJobsProvider()))
	tests := []struct {
		name       string
		username   string
		wantStatus int
	}{
		{name: "anonymous", wantStatus: http.StatusUnauthorized},
		{name: "unbound", username: "unbound", wantStatus: http.StatusForbidden},
		{name: "viewer", username: "viewer", wantStatus: http.StatusForbidden},
		{name: "site-scoped-viewer", username: "siteviewer", wantStatus: http.StatusForbidden},
		{name: "auditor", username: "auditor", wantStatus: http.StatusOK},
		{name: "operator", username: "operator", wantStatus: http.StatusOK},
		{name: "administrator", username: "admin", wantStatus: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/ui/changes-jobs", nil)
			if test.username != "" {
				request.AddCookie(fixture.login(t, test.username))
			}
			result := httptest.NewRecorder()
			fixture.handler.ServeHTTP(result, request)
			if result.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", result.Code, test.wantStatus, result.Body.String())
			}
			if result.Code == http.StatusOK {
				if !strings.Contains(result.Body.String(), `"state":"current"`) {
					t.Fatalf("current state missing: %s", result.Body.String())
				}
				if got := result.Header().Get("Cache-Control"); got != "no-store" {
					t.Fatalf("Cache-Control=%q", got)
				}
			}
		})
	}
}

func TestChangesJobsEndpointKeepsUnavailableProviderFailClosed(t *testing.T) {
	provider := productui.ChangesJobsProviderFunc(func(context.Context) (productui.ChangesJobsView, error) {
		return productui.ChangesJobsView{
			ContractVersion: productui.ChangesJobsContractVersion,
			State:           productui.ChangesJobsUnavailable,
			Changes:         []productui.ChangeOperationalView{},
		}, nil
	})
	fixture := newResourceAuthFixtureWithProductOptions(t, withChangesJobsProvider(provider))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/ui/changes-jobs", nil)
	request.AddCookie(fixture.login(t, "auditor"))
	result := httptest.NewRecorder()
	fixture.handler.ServeHTTP(result, request)
	if result.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want=%d body=%s", result.Code, http.StatusServiceUnavailable, result.Body.String())
	}
	if !strings.Contains(result.Body.String(), `"state":"unavailable"`) || strings.Contains(result.Body.String(), `"state":"current"`) {
		t.Fatalf("unavailable provider was rendered optimistically: %s", result.Body.String())
	}
}

func currentChangesJobsProvider() productui.ChangesJobsProvider {
	generatedAt := time.Date(2026, 9, 12, 6, 40, 0, 0, time.UTC)
	return productui.ChangesJobsProviderFunc(func(context.Context) (productui.ChangesJobsView, error) {
		return productui.ChangesJobsView{
			ContractVersion: productui.ChangesJobsContractVersion,
			State:           productui.ChangesJobsCurrent,
			GeneratedAt:     &generatedAt,
			ChangeCount:     0,
			JobCount:        0,
			Changes:         []productui.ChangeOperationalView{},
		}, nil
	})
}
