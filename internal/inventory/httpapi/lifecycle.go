package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"control-center/internal/inventory"
)

const maxLifecycleRequestBytes = 256 << 10

type observationPayload struct {
	DeviceID string    `json:"device_id"`
	Source   string    `json:"source"`
	Hostname string    `json:"hostname,omitempty"`
	SeenAt   time.Time `json:"seen_at"`
}

type reconcileRequest struct {
	Observations []observationPayload `json:"observations"`
}

type reconciledPayload struct {
	DeviceID string             `json:"device_id"`
	Latest   observationPayload `json:"latest"`
	Sources  []string           `json:"sources"`
}

type freshnessRequest struct {
	ObservedAt         time.Time `json:"observed_at"`
	Now                time.Time `json:"now"`
	StaleAfterSeconds  int64     `json:"stale_after_seconds"`
	ExpireAfterSeconds int64     `json:"expire_after_seconds"`
}

func ReconcileHandler() http.Handler {
	return strictJSONPost(maxLifecycleRequestBytes, func(w http.ResponseWriter, r *http.Request, decoder *json.Decoder) {
		var input reconcileRequest
		if err := decoder.Decode(&input); err != nil || ensureInventoryEOF(decoder) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		observations := make([]inventory.DeviceObservation, 0, len(input.Observations))
		for _, item := range input.Observations {
			observations = append(observations, inventory.DeviceObservation{
				DeviceID: item.DeviceID, Source: item.Source, Hostname: item.Hostname, SeenAt: item.SeenAt,
			})
		}
		result, err := inventory.ReconcileObservations(observations)
		if err != nil {
			http.Error(w, "invalid observation", http.StatusUnprocessableEntity)
			return
		}
		items := make([]reconciledPayload, 0, len(result))
		for _, item := range result {
			items = append(items, reconciledPayload{
				DeviceID: item.DeviceID,
				Latest:   observationPayload{DeviceID: item.Latest.DeviceID, Source: item.Latest.Source, Hostname: item.Latest.Hostname, SeenAt: item.Latest.SeenAt},
				Sources:  item.Sources,
			})
		}
		writeInventoryJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
	})
}

func FreshnessHandler() http.Handler {
	return strictJSONPost(maxLifecycleRequestBytes, func(w http.ResponseWriter, r *http.Request, decoder *json.Decoder) {
		var input freshnessRequest
		if err := decoder.Decode(&input); err != nil || ensureInventoryEOF(decoder) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if input.ObservedAt.IsZero() || input.Now.IsZero() || input.StaleAfterSeconds < 0 || input.ExpireAfterSeconds < input.StaleAfterSeconds {
			http.Error(w, "invalid freshness policy", http.StatusUnprocessableEntity)
			return
		}
		state := inventory.EvaluateFreshness(
			input.ObservedAt, input.Now,
			time.Duration(input.StaleAfterSeconds)*time.Second,
			time.Duration(input.ExpireAfterSeconds)*time.Second,
		)
		writeInventoryJSON(w, http.StatusOK, map[string]any{"state": state})
	})
}

func strictJSONPost(limit int64, handler func(http.ResponseWriter, *http.Request, *json.Decoder)) http.Handler {
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
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		handler(w, r, decoder)
	})
}

func ensureInventoryEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else {
		return err
	}
}

func writeInventoryJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
