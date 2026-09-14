package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	productui "control-center/internal/ui"
)

type operationalReportProviderFunc func(context.Context) (productui.OperationalReport, error)

func (f operationalReportProviderFunc) OperationalReport(ctx context.Context) (productui.OperationalReport, error) {
	return f(ctx)
}

func testOperationalReport(t *testing.T) productui.OperationalReport {
	t.Helper()
	now := time.Date(2026, 9, 13, 0, 45, 0, 0, time.UTC)
	report, err := productui.BuildOperationalReport(now, productui.ReportDataLoaded, []productui.ReportEvidenceInput{
		{
			ID:            "health:node-1",
			Kind:          "health",
			ResourceKind:  "node",
			ResourceID:    "node-1",
			ObservedState: productui.ReportHealthDegraded,
			Freshness:     productui.ReportFreshnessCurrent,
			ObservedAt:    now.Add(-time.Minute),
			ReasonCode:    "health_degraded",
		},
		{
			ID:            "health:node-2",
			Kind:          "health",
			ResourceKind:  "node",
			ResourceID:    "node-2",
			ObservedState: productui.ReportHealthHealthy,
			Freshness:     productui.ReportFreshnessCurrent,
			ObservedAt:    now.Add(-time.Minute),
		},
	})
	if err != nil {
		t.Fatalf("BuildOperationalReport() error = %v", err)
	}
	return report
}

func TestOperationalReportHandlerReturnsOnlyCanonicalReadOnlyReport(t *testing.T) {
	report := testOperationalReport(t)
	handler := OperationalReportHandler(operationalReportProviderFunc(func(context.Context) (productui.OperationalReport, error) {
		return report, nil
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/ui/reports/health", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	var got productui.OperationalReport
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.MutationAuthorized {
		t.Fatal("MutationAuthorized = true, want false")
	}
	if got.EvidenceDigest != report.EvidenceDigest {
		t.Fatalf("EvidenceDigest = %q, want %q", got.EvidenceDigest, report.EvidenceDigest)
	}
}

func TestOperationalReportHandlerFailsClosedForUnavailableOrTamperedProvider(t *testing.T) {
	t.Run("nil provider", func(t *testing.T) {
		response := httptest.NewRecorder()
		OperationalReportHandler(nil).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/ui/reports/health", nil))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
		}
	})

	t.Run("provider error", func(t *testing.T) {
		handler := OperationalReportHandler(operationalReportProviderFunc(func(context.Context) (productui.OperationalReport, error) {
			return productui.OperationalReport{}, errors.New("backend unavailable")
		}))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/ui/reports/health", nil))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
		}
	})

	t.Run("tampered projection", func(t *testing.T) {
		report := testOperationalReport(t)
		report.OverallState = productui.ReportHealthHealthy
		handler := OperationalReportHandler(operationalReportProviderFunc(func(context.Context) (productui.OperationalReport, error) {
			return report, nil
		}))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/ui/reports/health", nil))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
		}
	})
}

func TestOperationalReportHandlerRejectsMethodAndClientSelectedQuery(t *testing.T) {
	report := testOperationalReport(t)
	handler := OperationalReportHandler(operationalReportProviderFunc(func(context.Context) (productui.OperationalReport, error) {
		return report, nil
	}))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/ui/reports/health", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/ui/reports/health?source=audit", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("query status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestEvidenceDrawerHandlerUsesExactResourceBindingWithoutCrossResourceLeakage(t *testing.T) {
	report := testOperationalReport(t)
	handler := EvidenceDrawerHandler(operationalReportProviderFunc(func(context.Context) (productui.OperationalReport, error) {
		return report, nil
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/ui/reports/health/evidence?resource_kind=node&resource_id=node-1", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	var drawer productui.EvidenceDrawer
	if err := json.Unmarshal(response.Body.Bytes(), &drawer); err != nil {
		t.Fatalf("decode drawer: %v", err)
	}
	if drawer.ResourceKind != "node" || drawer.ResourceID != "node-1" {
		t.Fatalf("resource = %s/%s, want node/node-1", drawer.ResourceKind, drawer.ResourceID)
	}
	if len(drawer.Evidence) != 1 || drawer.Evidence[0].ResourceID != "node-1" {
		t.Fatalf("drawer leaked cross-resource evidence: %#v", drawer.Evidence)
	}
	if drawer.MutationAuthorized {
		t.Fatal("MutationAuthorized = true, want false")
	}
}

func TestEvidenceDrawerHandlerRejectsAmbiguousOrUnknownQuery(t *testing.T) {
	report := testOperationalReport(t)
	handler := EvidenceDrawerHandler(operationalReportProviderFunc(func(context.Context) (productui.OperationalReport, error) {
		return report, nil
	}))

	for _, target := range []string{
		"/evidence",
		"/evidence?resource_kind=node",
		"/evidence?resource_kind=node&resource_id=node-1&extra=1",
		"/evidence?resource_kind=node&resource_kind=site&resource_id=node-1",
		"/evidence?resource_kind=node&resource_id=node-1&resource_id=node-2",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want %d", target, response.Code, http.StatusBadRequest)
		}
	}
}
