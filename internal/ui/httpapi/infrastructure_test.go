package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	productui "control-center/internal/ui"
)

func TestInfrastructureHandlerFailsClosedWithoutProvider(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/ui/infrastructure", nil)
	w := httptest.NewRecorder()
	InfrastructureHandler(nil).ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"state":"unavailable"`) || strings.Contains(w.Body.String(), `"state":"current"`) {
		t.Fatalf("body=%s", w.Body.String())
	}
}

func TestInfrastructureHandlerRejectsInvalidProviderEnvelope(t *testing.T) {
	provider := productui.InfrastructureInventoryProviderFunc(func(context.Context) (productui.InfrastructureInventory, error) {
		return productui.InfrastructureInventory{
			ContractVersion: productui.InfrastructureInventoryContractVersion,
			State:           productui.InventoryViewCurrent,
			SiteCount:       0,
			NodeCount:       0,
			Sites:           []productui.SiteInventory{},
		}, nil
	})
	r := httptest.NewRequest(http.MethodGet, "/api/v1/ui/infrastructure", nil)
	w := httptest.NewRecorder()
	InfrastructureHandler(provider).ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestInfrastructureHandlerReturnsValidatedLoadedSnapshot(t *testing.T) {
	generatedAt := time.Date(2026, 9, 12, 1, 0, 0, 0, time.UTC)
	provider := productui.InfrastructureInventoryProviderFunc(func(context.Context) (productui.InfrastructureInventory, error) {
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
	r := httptest.NewRequest(http.MethodGet, "/api/v1/ui/infrastructure", nil)
	w := httptest.NewRecorder()
	InfrastructureHandler(provider).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"site_count":1`) || !strings.Contains(w.Body.String(), `"state":"current"`) {
		t.Fatalf("body=%s", w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q", got)
	}
}

func TestInfrastructureHandlerHidesProviderErrors(t *testing.T) {
	provider := productui.InfrastructureInventoryProviderFunc(func(context.Context) (productui.InfrastructureInventory, error) {
		return productui.InfrastructureInventory{}, errors.New("database password leaked in internal error")
	})
	r := httptest.NewRequest(http.MethodGet, "/api/v1/ui/infrastructure", nil)
	w := httptest.NewRecorder()
	InfrastructureHandler(provider).ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "database") {
		t.Fatalf("provider error leaked: %s", w.Body.String())
	}
}

func TestInfrastructureHandlerRejectsMutationMethods(t *testing.T) {
	provider := productui.InfrastructureInventoryProviderFunc(func(context.Context) (productui.InfrastructureInventory, error) {
		return productui.InfrastructureInventory{}, nil
	})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/ui/infrastructure", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	InfrastructureHandler(provider).ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("status=%d allow=%q", w.Code, w.Header().Get("Allow"))
	}
}
