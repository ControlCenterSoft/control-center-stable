package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"control-center/internal/domain"
)

const maxProviderRequestBytes = 64 << 10

type providerRequest struct {
	Preferred    domain.Provider     `json:"preferred"`
	Requirements domain.Requirements `json:"requirements"`
}

type providerResponse struct {
	Provider domain.Provider `json:"provider"`
}

func ProviderHandler() http.Handler {
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
		var input providerRequest
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := ensureEOF(decoder); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		provider, err := domain.ResolveProvider(input.Preferred, input.Requirements)
		if err != nil {
			if errors.Is(err, domain.ErrIncompatibleProvider) {
				http.Error(w, "incompatible provider", http.StatusUnprocessableEntity)
				return
			}
			http.Error(w, "unable to resolve provider", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(providerResponse{Provider: provider})
	})
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values are not allowed")
	}
	return err
}
