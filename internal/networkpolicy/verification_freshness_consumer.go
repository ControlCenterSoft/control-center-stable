package networkpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// ParseVerificationEvidence is the strict serialized consumer boundary for
// network verification freshness evidence. It validates only immutable shape
// and canonical identifiers; freshness remains a separate evaluation against
// an explicit trusted time and policy.
func ParseVerificationEvidence(data []byte) (VerificationEvidence, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return VerificationEvidence{}, fmt.Errorf("%w: verification document is required", ErrInvalidVerificationEvidence)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var evidence VerificationEvidence
	if err := decoder.Decode(&evidence); err != nil {
		return VerificationEvidence{}, fmt.Errorf("%w: decode verification evidence: %v", ErrInvalidVerificationEvidence, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return VerificationEvidence{}, fmt.Errorf("%w: trailing JSON document", ErrInvalidVerificationEvidence)
		}
		return VerificationEvidence{}, fmt.Errorf("%w: trailing JSON: %v", ErrInvalidVerificationEvidence, err)
	}
	if err := validateVerificationEvidenceConsumerShape(evidence); err != nil {
		return VerificationEvidence{}, err
	}
	return evidence, nil
}

func validateVerificationEvidenceConsumerShape(evidence VerificationEvidence) error {
	if evidence.SchemaVersion != VerificationFreshnessSchemaVersion {
		return fmt.Errorf("%w: unsupported schema_version %q", ErrInvalidVerificationEvidence, evidence.SchemaVersion)
	}
	if err := validateDigestID("plan_id", evidence.PlanID); err != nil {
		return err
	}
	revisionID, err := normalizeIdentifier("revision_id", evidence.RevisionID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidVerificationEvidence, err)
	}
	if evidence.RevisionID != revisionID {
		return fmt.Errorf("%w: revision_id must be canonical", ErrInvalidVerificationEvidence)
	}
	if evidence.VerifiedAt.IsZero() {
		return fmt.Errorf("%w: verified_at is required", ErrInvalidVerificationEvidence)
	}
	if len(evidence.Checks) == 0 || len(evidence.Checks) > maxVerificationChecks {
		return fmt.Errorf("%w: checks must contain 1..%d entries", ErrInvalidVerificationEvidence, maxVerificationChecks)
	}

	seen := make(map[string]struct{}, len(evidence.Checks))
	for _, check := range evidence.Checks {
		name, err := normalizeIdentifier("check name", check.Name)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidVerificationEvidence, err)
		}
		if check.Name != name {
			return fmt.Errorf("%w: check name %q must be canonical", ErrInvalidVerificationEvidence, check.Name)
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("%w: duplicate check %q", ErrInvalidVerificationEvidence, name)
		}
		seen[name] = struct{}{}
		if check.Status != VerificationCheckPass && check.Status != VerificationCheckFail {
			return fmt.Errorf("%w: check %q has invalid status %q", ErrInvalidVerificationEvidence, name, check.Status)
		}
		if err := validateDigestID("check evidence_digest", check.EvidenceDigest); err != nil {
			return err
		}
		if check.ObservedAt.IsZero() {
			return fmt.Errorf("%w: check %q observed_at is required", ErrInvalidVerificationEvidence, name)
		}
	}
	return nil
}
