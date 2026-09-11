package networkpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// ParseVerificationPreflightAdmission is the strict consumer boundary for a
// serialized preflight admission. It rejects unknown/trailing JSON and applies
// the same bounded identifier rules as the published schema before accepting
// the admission identity. Parsed admissions remain non-authorizing evidence.
func ParseVerificationPreflightAdmission(data []byte) (VerificationPreflightAdmission, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return VerificationPreflightAdmission{}, fmt.Errorf("%w: admission document is required", ErrInvalidVerificationPreflightAdmission)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var admission VerificationPreflightAdmission
	if err := decoder.Decode(&admission); err != nil {
		return VerificationPreflightAdmission{}, fmt.Errorf("%w: decode admission: %v", ErrInvalidVerificationPreflightAdmission, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return VerificationPreflightAdmission{}, fmt.Errorf("%w: trailing JSON document", ErrInvalidVerificationPreflightAdmission)
		}
		return VerificationPreflightAdmission{}, fmt.Errorf("%w: trailing JSON: %v", ErrInvalidVerificationPreflightAdmission, err)
	}
	if err := validateVerificationPreflightAdmissionConsumerShape(admission); err != nil {
		return VerificationPreflightAdmission{}, err
	}
	if err := validateVerificationPreflightAdmission(admission); err != nil {
		return VerificationPreflightAdmission{}, err
	}
	return admission, nil
}

func validateVerificationPreflightAdmissionConsumerShape(admission VerificationPreflightAdmission) error {
	lists := []struct {
		name   string
		checks []string
	}{
		{name: "missing_checks", checks: admission.MissingChecks},
		{name: "stale_checks", checks: admission.StaleChecks},
		{name: "failed_checks", checks: admission.FailedChecks},
	}
	for _, list := range lists {
		if len(list.checks) > maxVerificationChecks {
			return fmt.Errorf("%w: %s exceeds %d entries", ErrInvalidVerificationPreflightAdmission, list.name, maxVerificationChecks)
		}
		seen := make(map[string]struct{}, len(list.checks))
		previous := ""
		for index, raw := range list.checks {
			name, err := normalizeIdentifier(list.name, raw)
			if err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidVerificationPreflightAdmission, err)
			}
			if name != raw {
				return fmt.Errorf("%w: %s contains non-canonical check name %q", ErrInvalidVerificationPreflightAdmission, list.name, raw)
			}
			if index > 0 && name < previous {
				return fmt.Errorf("%w: %s must be sorted", ErrInvalidVerificationPreflightAdmission, list.name)
			}
			if _, duplicate := seen[name]; duplicate {
				return fmt.Errorf("%w: %s contains duplicate check %q", ErrInvalidVerificationPreflightAdmission, list.name, name)
			}
			seen[name] = struct{}{}
			previous = name
		}
	}
	if !admission.Ready && len(admission.MissingChecks)+len(admission.StaleChecks)+len(admission.FailedChecks) == 0 {
		return fmt.Errorf("%w: rejected admission requires at least one rejection reason", ErrInvalidVerificationPreflightAdmission)
	}
	return nil
}
