package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"control-center/internal/agent"
)

const maxEnrollmentRequestBytes = 256 << 10

func EnrollmentHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
			return
		}
		var input agent.EnrollmentRequest
		if err := decodeEnrollmentJSON(w, r, &input); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		normalized, err := agent.NormalizeEnrollmentContract(input)
		if err != nil {
			if errors.Is(err, agent.ErrInvalidEnrollment) {
				http.Error(w, "invalid enrollment", http.StatusUnprocessableEntity)
				return
			}
			http.Error(w, "unable to normalize enrollment", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(normalized)
	})
}

func decodeEnrollmentJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxEnrollmentRequestBytes)
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if err := rejectDuplicateJSONKeys(payload); err != nil {
		return err
	}
	if err := rejectLegacyExtendedFields(payload); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON value")
	}
	return nil
}

func rejectLegacyExtendedFields(payload []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return err
	}
	version := ""
	if rawVersion, exists := fields["contract_version"]; exists {
		if err := json.Unmarshal(rawVersion, &version); err != nil {
			return err
		}
		version = strings.ToLower(strings.TrimSpace(version))
	}
	if version != "" && version != agent.EnrollmentContractV1 {
		return nil
	}
	for _, field := range []string{
		"roles", "site_id", "management_zone_id", "collected_at", "hardware",
		"network_interfaces", "identity", "capacity_observations",
	} {
		if _, exists := fields[field]; exists {
			return fmt.Errorf("extended field %q requires contract_version %q", field, agent.EnrollmentContractV2)
		}
	}
	return nil
}

func rejectDuplicateJSONKeys(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON token")
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return fmt.Errorf("invalid JSON object")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return fmt.Errorf("invalid JSON array")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}
