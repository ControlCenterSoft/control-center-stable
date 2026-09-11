package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"control-center/internal/inventory"
)

type stateObservationPayload struct {
	DeviceID string    `json:"device_id"`
	Source   string    `json:"source"`
	Hostname string    `json:"hostname"`
	SeenAt   time.Time `json:"seen_at"`
}

type observationsRequest struct {
	Observations []stateObservationPayload `json:"observations"`
}

func StateHandler(registry *inventory.MemoryRegistry) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/inventory/observations", func(w http.ResponseWriter, r *http.Request) {
		if registry == nil {
			http.Error(w, "inventory registry unavailable", http.StatusServiceUnavailable)
			return
		}
		if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var input observationsRequest
		if err := decoder.Decode(&input); err != nil || inventoryEOF(decoder) != nil || len(input.Observations) == 0 {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		observations := make([]inventory.DeviceObservation, 0, len(input.Observations))
		for _, item := range input.Observations {
			observations = append(observations, inventory.DeviceObservation{
				DeviceID: item.DeviceID, Source: item.Source, Hostname: item.Hostname, SeenAt: item.SeenAt,
			})
		}
		items, err := registry.Ingest(observations)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeInventoryJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
	})
	mux.HandleFunc("GET /api/v1/inventory/devices", func(w http.ResponseWriter, r *http.Request) {
		if registry == nil {
			http.Error(w, "inventory registry unavailable", http.StatusServiceUnavailable)
			return
		}
		items := registry.List()
		writeInventoryJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
	})
	mux.HandleFunc("GET /api/v1/inventory/devices/{deviceID}", func(w http.ResponseWriter, r *http.Request) {
		if registry == nil {
			http.Error(w, "inventory registry unavailable", http.StatusServiceUnavailable)
			return
		}
		item, ok := registry.Get(r.PathValue("deviceID"))
		if !ok {
			http.Error(w, "device not found", http.StatusNotFound)
			return
		}
		writeInventoryJSON(w, http.StatusOK, item)
	})
	return mux
}
