package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"control-center/internal/domain"
)

type joinRequest struct {
	Platform   string `json:"platform"`
	Provider   string `json:"provider"`
	DomainName string `json:"domain_name"`
}

type readinessRequest struct {
	Provider      string `json:"provider"`
	DNSReady      bool   `json:"dns_ready"`
	TimeSyncReady bool   `json:"time_sync_ready"`
	StorageReady  bool   `json:"storage_ready"`
}

func JoinValidationHandler() http.Handler {
	return strictPOST(func(w http.ResponseWriter, _ *http.Request, decoder *json.Decoder) {
		var input joinRequest
		if err := decoder.Decode(&input); err != nil || ensureEOF(decoder) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		err := domain.ValidateDirectoryJoin(domain.DirectoryJoinRequest{
			Platform: input.Platform, Provider: input.Provider, DomainName: input.DomainName,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"valid": true})
	})
}

func ReadinessHandler() http.Handler {
	return strictPOST(func(w http.ResponseWriter, _ *http.Request, decoder *json.Decoder) {
		var input readinessRequest
		if err := decoder.Decode(&input); err != nil || ensureEOF(decoder) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		result := domain.EvaluateDomainReadiness(input.Provider, input.DNSReady, input.TimeSyncReady, input.StorageReady)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}

// LifecyclePlanHandler exposes planning only. Execution and provider
// credentials are intentionally outside this boundary.
func LifecyclePlanHandler() http.Handler {
	return strictPOST(func(w http.ResponseWriter, _ *http.Request, decoder *json.Decoder) {
		var request domain.LifecyclePlanRequest
		if err := decoder.Decode(&request); err != nil || ensureEOF(decoder) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		plan, err := domain.BuildLifecyclePlan(request)
		if err != nil {
			http.Error(w, "invalid lifecycle plan request", http.StatusUnprocessableEntity)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(plan)
	})
}

type strictPOSTFunc func(http.ResponseWriter, *http.Request, *json.Decoder)

func strictPOST(next strictPOSTFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxProviderRequestBytes)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		next(w, r, decoder)
	})
}
