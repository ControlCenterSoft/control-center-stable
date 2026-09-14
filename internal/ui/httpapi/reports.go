package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"

	productui "control-center/internal/ui"
)

const reportsUnavailableError = "reports_unavailable"

// OperationalReportHandler exposes one already-authorized read-only report
// provider. The caller must bind this handler to the permission appropriate for
// that exact source. In particular, an Audit-backed provider must remain behind
// audit.events.read and must not be exposed through a weaker resources.read
// route merely because the output uses the common report contract.
func OperationalReportHandler(provider productui.OperationalReportProvider) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setReportHeaders(w)
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeReportJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
			return
		}
		if r.URL.RawQuery != "" {
			writeReportJSON(w, http.StatusBadRequest, map[string]string{"error": "query_not_allowed"})
			return
		}

		report, ok := loadOperationalReport(r, provider)
		if !ok {
			writeReportJSON(w, http.StatusServiceUnavailable, map[string]string{"error": reportsUnavailableError})
			return
		}
		if report.DataState == productui.ReportDataUnavailable {
			writeReportJSON(w, http.StatusServiceUnavailable, report)
			return
		}
		writeReportJSON(w, http.StatusOK, report)
	})
}

// EvidenceDrawerHandler projects one exact resource from an already-authorized
// report. Resource identity is supplied only as a strict query contract and is
// never used to broaden the provider's authorization scope.
func EvidenceDrawerHandler(provider productui.OperationalReportProvider) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setReportHeaders(w)
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeReportJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
			return
		}

		resourceKind, resourceID, ok := exactResourceQuery(r.URL.RawQuery)
		if !ok {
			writeReportJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_resource_query"})
			return
		}
		report, loaded := loadOperationalReport(r, provider)
		if !loaded {
			writeReportJSON(w, http.StatusServiceUnavailable, map[string]string{"error": reportsUnavailableError})
			return
		}
		drawer, err := productui.BuildEvidenceDrawer(report, resourceKind, resourceID)
		if err != nil {
			writeReportJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_resource_query"})
			return
		}
		if drawer.DataState == productui.ReportDataUnavailable {
			writeReportJSON(w, http.StatusServiceUnavailable, drawer)
			return
		}
		writeReportJSON(w, http.StatusOK, drawer)
	})
}

func loadOperationalReport(r *http.Request, provider productui.OperationalReportProvider) (productui.OperationalReport, bool) {
	if provider == nil {
		return productui.OperationalReport{}, false
	}
	report, err := provider.OperationalReport(r.Context())
	if err != nil || productui.ValidateOperationalReport(report) != nil {
		return productui.OperationalReport{}, false
	}
	return report, true
}

func exactResourceQuery(raw string) (string, string, bool) {
	query, err := url.ParseQuery(raw)
	if err != nil || len(query) != 2 {
		return "", "", false
	}
	for key, values := range query {
		if key != "resource_kind" && key != "resource_id" {
			return "", "", false
		}
		if len(values) != 1 || values[0] == "" {
			return "", "", false
		}
	}
	kind, kindOK := query["resource_kind"]
	id, idOK := query["resource_id"]
	if !kindOK || !idOK || len(kind) != 1 || len(id) != 1 {
		return "", "", false
	}
	return kind[0], id[0], true
}

func setReportHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func writeReportJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
