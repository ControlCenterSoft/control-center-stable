package httpapi

import (
	"encoding/json"
	"net/http"

	productui "control-center/internal/ui"
)

// InfrastructureHandler exposes only the validated read model supplied by an
// authoritative provider. Provider failures and unavailable evidence are
// fail-closed and never become a successful empty inventory response.
func InfrastructureHandler(provider productui.InfrastructureInventoryProvider) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeInfrastructureError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		if provider == nil {
			writeInfrastructureUnavailable(w)
			return
		}
		view, err := provider.InfrastructureInventory(r.Context())
		if err != nil {
			writeInfrastructureUnavailable(w)
			return
		}
		if err := productui.ValidateInfrastructureInventory(view); err != nil {
			writeInfrastructureUnavailable(w)
			return
		}
		if view.State == productui.InventoryViewUnavailable {
			writeInfrastructureJSON(w, http.StatusServiceUnavailable, view)
			return
		}
		writeInfrastructureJSON(w, http.StatusOK, view)
	})
}

func writeInfrastructureUnavailable(w http.ResponseWriter) {
	writeInfrastructureJSON(w, http.StatusServiceUnavailable, productui.InfrastructureInventory{
		ContractVersion: productui.InfrastructureInventoryContractVersion,
		State:           productui.InventoryViewUnavailable,
		Sites:           []productui.SiteInventory{},
	})
}

func writeInfrastructureError(w http.ResponseWriter, status int, code string) {
	writeInfrastructureJSON(w, status, map[string]string{"error": code})
}

func writeInfrastructureJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
