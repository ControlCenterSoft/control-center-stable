package httpapi

import (
	"encoding/json"
	"net/http"

	productui "control-center/internal/ui"
)

// ChangesJobsHandler exposes only validated persisted orchestration state.
// Provider failures and unavailable evidence remain fail-closed; this handler
// has no approve/queue/cancel/retry/execution authority.
func ChangesJobsHandler(provider productui.ChangesJobsProvider) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeChangesJobsJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
			return
		}
		if provider == nil {
			writeChangesJobsUnavailable(w)
			return
		}
		view, err := provider.ChangesJobs(r.Context())
		if err != nil || productui.ValidateChangesJobsView(view) != nil {
			writeChangesJobsUnavailable(w)
			return
		}
		if view.State == productui.ChangesJobsUnavailable {
			writeChangesJobsJSON(w, http.StatusServiceUnavailable, view)
			return
		}
		writeChangesJobsJSON(w, http.StatusOK, view)
	})
}

func writeChangesJobsUnavailable(w http.ResponseWriter) {
	writeChangesJobsJSON(w, http.StatusServiceUnavailable, productui.ChangesJobsView{
		ContractVersion: productui.ChangesJobsContractVersion,
		State:           productui.ChangesJobsUnavailable,
		Changes:         []productui.ChangeOperationalView{},
	})
}

func writeChangesJobsJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
