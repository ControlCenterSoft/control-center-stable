package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"control-center/internal/inventory"
)

const maxInventoryRequestBytes = 128 << 10

type devicePayload struct {
	Hostname  string             `json:"hostname"`
	Platform  inventory.Platform `json:"platform"`
	MachineID string             `json:"machine_id,omitempty"`
	Serial    string             `json:"serial,omitempty"`
	Addresses []string           `json:"addresses,omitempty"`
	Tags      []string           `json:"tags,omitempty"`
}

func NormalizeHandler() http.Handler {
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
		r.Body = http.MaxBytesReader(w, r.Body, maxInventoryRequestBytes)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var input devicePayload
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := inventoryEOF(decoder); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		device, err := inventory.NormalizeDevice(inventory.Device{
			Hostname: input.Hostname, Platform: input.Platform, MachineID: input.MachineID,
			Serial: input.Serial, Addresses: input.Addresses, Tags: input.Tags,
		})
		if err != nil {
			if errors.Is(err, inventory.ErrInvalidDevice) {
				http.Error(w, "invalid device", http.StatusUnprocessableEntity)
				return
			}
			http.Error(w, "unable to normalize device", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(devicePayload{
			Hostname: device.Hostname, Platform: device.Platform, MachineID: device.MachineID,
			Serial: device.Serial, Addresses: device.Addresses, Tags: device.Tags,
		})
	})
}

func inventoryEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else {
		return err
	}
}
