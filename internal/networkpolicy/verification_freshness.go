package networkpolicy

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const VerificationFreshnessSchemaVersion = "network.change.verification-freshness/v1"

const maxVerificationChecks = maxProbesPerPlan

var ErrInvalidVerificationEvidence = errors.New("invalid network verification evidence")

type VerificationCheckStatus string

const (
	VerificationCheckPass VerificationCheckStatus = "PASS"
	VerificationCheckFail VerificationCheckStatus = "FAIL"
)

// VerificationCheckEvidence is immutable evidence for one typed safety check.
// It carries no command, credential, endpoint or execution authority.
type VerificationCheckEvidence struct {
	Name           string                  `json:"name"`
	Status         VerificationCheckStatus `json:"status"`
	EvidenceDigest string                  `json:"evidence_digest"`
	ObservedAt     time.Time               `json:"observed_at"`
}

// VerificationEvidence binds a verification result to one exact change plan
// and revision. The contract is read-only and cannot authorize execution.
type VerificationEvidence struct {
	SchemaVersion string                      `json:"schema_version"`
	PlanID        string                      `json:"plan_id"`
	RevisionID    string                      `json:"revision_id"`
	VerifiedAt    time.Time                   `json:"verified_at"`
	Checks        []VerificationCheckEvidence `json:"checks"`
}

type VerificationFreshnessPolicy struct {
	MaxAge         time.Duration `json:"max_age"`
	MaxFutureSkew  time.Duration `json:"max_future_skew"`
	RequiredChecks []string      `json:"required_checks"`
}

// ChangePlanVerificationFreshnessPolicy controls only time freshness. Required
// checks are derived from the exact canonical ChangePlan so a caller cannot
// weaken verification by supplying a shorter required-check list.
type ChangePlanVerificationFreshnessPolicy struct {
	MaxAge        time.Duration `json:"max_age"`
	MaxFutureSkew time.Duration `json:"max_future_skew"`
}

// VerificationFreshnessVerdict is deterministic, fail-closed eligibility
// evidence. Ready=true means only that the supplied verification evidence is
// current and complete; it never grants permission to mutate networking.
type VerificationFreshnessVerdict struct {
	Ready         bool     `json:"ready"`
	MissingChecks []string `json:"missing_checks,omitempty"`
	StaleChecks   []string `json:"stale_checks,omitempty"`
	FailedChecks  []string `json:"failed_checks,omitempty"`
}

// EvaluateChangePlanVerificationFreshness evaluates verification evidence for
// one exact canonical network change plan. Every probe declared by that plan is
// mandatory and is addressed by its unique probe ID. Both the supplied plan
// identity and its canonical reconstruction are verified before evidence can be
// considered, so mutated envelope fields cannot reuse an older PlanID. Plan-
// bound evidence is exact: checks not declared by the plan are rejected rather
// than silently ignored.
func EvaluateChangePlanVerificationFreshness(
	now time.Time,
	plan ChangePlan,
	evidence VerificationEvidence,
	policy ChangePlanVerificationFreshnessPolicy,
) (VerificationFreshnessVerdict, error) {
	if plan.PlanID == "" || changePlanID(plan) != plan.PlanID {
		return VerificationFreshnessVerdict{}, fmt.Errorf("%w: change plan identity mismatch", ErrInvalidVerificationEvidence)
	}

	validated, err := BuildChangePlan(ChangePlanRequest{
		NodeID:     plan.NodeID,
		RevisionID: plan.RevisionID,
		Interfaces: plan.Interfaces,
		Forwarding: plan.Forwarding,
		Probes:     plan.Probes,
		Timeouts:   plan.Timeouts,
	})
	if err != nil || validated.PlanID != plan.PlanID {
		return VerificationFreshnessVerdict{}, fmt.Errorf("%w: change plan integrity check failed", ErrInvalidVerificationEvidence)
	}

	requiredChecks := make([]string, 0, len(validated.Probes))
	requiredSet := make(map[string]struct{}, len(validated.Probes))
	for _, probe := range validated.Probes {
		requiredChecks = append(requiredChecks, probe.ID)
		requiredSet[probe.ID] = struct{}{}
	}
	for _, check := range evidence.Checks {
		name, err := normalizeIdentifier("check name", check.Name)
		if err != nil {
			return VerificationFreshnessVerdict{}, fmt.Errorf("%w: %v", ErrInvalidVerificationEvidence, err)
		}
		if check.Name != name {
			return VerificationFreshnessVerdict{}, fmt.Errorf("%w: check name %q must be canonical", ErrInvalidVerificationEvidence, check.Name)
		}
		if _, declared := requiredSet[name]; !declared {
			return VerificationFreshnessVerdict{}, fmt.Errorf("%w: unexpected check %q for change plan", ErrInvalidVerificationEvidence, name)
		}
	}

	return EvaluateVerificationFreshness(
		now,
		validated.PlanID,
		validated.RevisionID,
		evidence,
		VerificationFreshnessPolicy{
			MaxAge:         policy.MaxAge,
			MaxFutureSkew:  policy.MaxFutureSkew,
			RequiredChecks: requiredChecks,
		},
	)
}

// EvaluateVerificationFreshness verifies that safety evidence belongs to the
// exact plan/revision and that every required check is present, PASS and fresh.
// Freshness windows use an exclusive expiry boundary: evidence is stale when
// observed_at + max_age is equal to or earlier than the evaluation time.
func EvaluateVerificationFreshness(
	now time.Time,
	expectedPlanID string,
	expectedRevisionID string,
	evidence VerificationEvidence,
	policy VerificationFreshnessPolicy,
) (VerificationFreshnessVerdict, error) {
	if now.IsZero() {
		return VerificationFreshnessVerdict{}, fmt.Errorf("%w: now is required", ErrInvalidVerificationEvidence)
	}
	if err := validateDigestID("expected plan_id", expectedPlanID); err != nil {
		return VerificationFreshnessVerdict{}, err
	}
	revisionID, err := normalizeIdentifier("expected revision_id", expectedRevisionID)
	if err != nil {
		return VerificationFreshnessVerdict{}, fmt.Errorf("%w: %v", ErrInvalidVerificationEvidence, err)
	}
	if revisionID != expectedRevisionID {
		return VerificationFreshnessVerdict{}, fmt.Errorf("%w: expected revision_id must be canonical", ErrInvalidVerificationEvidence)
	}
	if evidence.SchemaVersion != VerificationFreshnessSchemaVersion {
		return VerificationFreshnessVerdict{}, fmt.Errorf("%w: unsupported schema_version %q", ErrInvalidVerificationEvidence, evidence.SchemaVersion)
	}
	if err := validateDigestID("evidence plan_id", evidence.PlanID); err != nil {
		return VerificationFreshnessVerdict{}, err
	}
	if evidence.PlanID != expectedPlanID {
		return VerificationFreshnessVerdict{}, fmt.Errorf("%w: plan_id mismatch", ErrInvalidVerificationEvidence)
	}
	evidenceRevisionID, err := normalizeIdentifier("evidence revision_id", evidence.RevisionID)
	if err != nil {
		return VerificationFreshnessVerdict{}, fmt.Errorf("%w: %v", ErrInvalidVerificationEvidence, err)
	}
	if evidenceRevisionID != evidence.RevisionID {
		return VerificationFreshnessVerdict{}, fmt.Errorf("%w: evidence revision_id must be canonical", ErrInvalidVerificationEvidence)
	}
	if evidenceRevisionID != revisionID {
		return VerificationFreshnessVerdict{}, fmt.Errorf("%w: revision_id mismatch", ErrInvalidVerificationEvidence)
	}
	if policy.MaxAge <= 0 || policy.MaxAge > 24*time.Hour {
		return VerificationFreshnessVerdict{}, fmt.Errorf("%w: max_age must be greater than zero and at most 24h", ErrInvalidVerificationEvidence)
	}
	if policy.MaxFutureSkew < 0 || policy.MaxFutureSkew > 5*time.Minute {
		return VerificationFreshnessVerdict{}, fmt.Errorf("%w: max_future_skew must be between 0 and 5m", ErrInvalidVerificationEvidence)
	}
	if evidence.VerifiedAt.IsZero() {
		return VerificationFreshnessVerdict{}, fmt.Errorf("%w: verified_at is required", ErrInvalidVerificationEvidence)
	}
	if evidence.VerifiedAt.After(now.Add(policy.MaxFutureSkew)) {
		return VerificationFreshnessVerdict{}, fmt.Errorf("%w: verified_at is in the future", ErrInvalidVerificationEvidence)
	}

	required, err := canonicalRequiredChecks(policy.RequiredChecks)
	if err != nil {
		return VerificationFreshnessVerdict{}, err
	}
	checks, err := canonicalVerificationChecks(evidence.Checks, evidence.VerifiedAt, now, policy.MaxFutureSkew)
	if err != nil {
		return VerificationFreshnessVerdict{}, err
	}

	verdict := VerificationFreshnessVerdict{}
	if !evidence.VerifiedAt.Add(policy.MaxAge).After(now) {
		verdict.StaleChecks = append(verdict.StaleChecks, "verification")
	}
	for _, name := range required {
		check, exists := checks[name]
		if !exists {
			verdict.MissingChecks = append(verdict.MissingChecks, name)
			continue
		}
		if check.Status != VerificationCheckPass {
			verdict.FailedChecks = append(verdict.FailedChecks, name)
		}
		if !check.ObservedAt.Add(policy.MaxAge).After(now) {
			verdict.StaleChecks = append(verdict.StaleChecks, name)
		}
	}
	sort.Strings(verdict.MissingChecks)
	sort.Strings(verdict.StaleChecks)
	sort.Strings(verdict.FailedChecks)
	verdict.Ready = len(verdict.MissingChecks) == 0 && len(verdict.StaleChecks) == 0 && len(verdict.FailedChecks) == 0
	return verdict, nil
}

func canonicalRequiredChecks(input []string) ([]string, error) {
	if len(input) == 0 || len(input) > maxVerificationChecks {
		return nil, fmt.Errorf("%w: required_checks must contain 1..%d entries", ErrInvalidVerificationEvidence, maxVerificationChecks)
	}
	result := make([]string, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, raw := range input {
		name, err := normalizeIdentifier("required check", raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidVerificationEvidence, err)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("%w: duplicate required check %q", ErrInvalidVerificationEvidence, name)
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func canonicalVerificationChecks(
	input []VerificationCheckEvidence,
	verifiedAt time.Time,
	now time.Time,
	maxFutureSkew time.Duration,
) (map[string]VerificationCheckEvidence, error) {
	if len(input) == 0 || len(input) > maxVerificationChecks {
		return nil, fmt.Errorf("%w: checks must contain 1..%d entries", ErrInvalidVerificationEvidence, maxVerificationChecks)
	}
	result := make(map[string]VerificationCheckEvidence, len(input))
	for _, check := range input {
		name, err := normalizeIdentifier("check name", check.Name)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidVerificationEvidence, err)
		}
		if check.Name != name {
			return nil, fmt.Errorf("%w: check name %q must be canonical", ErrInvalidVerificationEvidence, check.Name)
		}
		check.Name = name
		if _, duplicate := result[name]; duplicate {
			return nil, fmt.Errorf("%w: duplicate check %q", ErrInvalidVerificationEvidence, name)
		}
		if check.Status != VerificationCheckPass && check.Status != VerificationCheckFail {
			return nil, fmt.Errorf("%w: check %q has invalid status %q", ErrInvalidVerificationEvidence, name, check.Status)
		}
		if err := validateDigestID("check evidence_digest", check.EvidenceDigest); err != nil {
			return nil, err
		}
		if check.ObservedAt.IsZero() {
			return nil, fmt.Errorf("%w: check %q observed_at is required", ErrInvalidVerificationEvidence, name)
		}
		if check.ObservedAt.After(now.Add(maxFutureSkew)) {
			return nil, fmt.Errorf("%w: check %q observed_at is in the future", ErrInvalidVerificationEvidence, name)
		}
		if check.ObservedAt.After(verifiedAt.Add(maxFutureSkew)) {
			return nil, fmt.Errorf("%w: check %q is newer than verification envelope", ErrInvalidVerificationEvidence, name)
		}
		result[name] = check
	}
	return result, nil
}

func validateDigestID(name, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed != value {
		return fmt.Errorf("%w: %s must be canonical without surrounding whitespace", ErrInvalidVerificationEvidence, name)
	}
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) {
		return fmt.Errorf("%w: %s must use sha256:<hex>", ErrInvalidVerificationEvidence, name)
	}
	raw := strings.TrimPrefix(value, prefix)
	if len(raw) != 64 {
		return fmt.Errorf("%w: %s must contain a 32-byte SHA-256 digest", ErrInvalidVerificationEvidence, name)
	}
	if _, err := hex.DecodeString(raw); err != nil {
		return fmt.Errorf("%w: %s contains invalid SHA-256 hex", ErrInvalidVerificationEvidence, name)
	}
	return nil
}
