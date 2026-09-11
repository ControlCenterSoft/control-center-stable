package networkpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const VerificationPreflightAdmissionSchemaVersion = "network.change.verification-preflight-admission/v1"

var ErrInvalidVerificationPreflightAdmission = errors.New("invalid network verification preflight admission")

// VerificationPreflightAdmission is deterministic, non-authorizing evidence
// that one exact ChangePlan has complete, passing and fresh connectivity
// verification for one exact preflight state-machine revision.
type VerificationPreflightAdmission struct {
	SchemaVersion             string    `json:"schema_version"`
	AdmissionID               string    `json:"admission_id"`
	PlanID                    string    `json:"plan_id"`
	RevisionID                string    `json:"revision_id"`
	EvidenceID                string    `json:"evidence_id"`
	MachineVersion            uint64    `json:"machine_version"`
	EvaluatedAt               time.Time `json:"evaluated_at"`
	ExpiresAt                 time.Time `json:"expires_at,omitempty"`
	Ready                     bool      `json:"ready"`
	MissingChecks             []string  `json:"missing_checks,omitempty"`
	StaleChecks               []string  `json:"stale_checks,omitempty"`
	FailedChecks              []string  `json:"failed_checks,omitempty"`
	ExecutionAuthorized       bool      `json:"execution_authorized"`
	ProductionMutationAllowed bool      `json:"production_mutation_allowed"`
}

// BuildVerificationPreflightAdmission binds freshness evidence to the exact
// preflight snapshot. It does not advance the state machine and does not grant
// network mutation authority.
func BuildVerificationPreflightAdmission(
	now time.Time,
	plan ChangePlan,
	snapshot ChangeSnapshot,
	evidence VerificationEvidence,
	policy ChangePlanVerificationFreshnessPolicy,
) (VerificationPreflightAdmission, error) {
	if now.IsZero() {
		return VerificationPreflightAdmission{}, fmt.Errorf("%w: evaluated_at is required", ErrInvalidVerificationPreflightAdmission)
	}
	if snapshot.PlanID != plan.PlanID {
		return VerificationPreflightAdmission{}, fmt.Errorf("%w: snapshot plan_id mismatch", ErrInvalidVerificationPreflightAdmission)
	}
	if snapshot.State != ChangeStatePreflight {
		return VerificationPreflightAdmission{}, fmt.Errorf("%w: snapshot state %q is not preflight", ErrInvalidVerificationPreflightAdmission, snapshot.State)
	}
	if snapshot.Version == 0 {
		return VerificationPreflightAdmission{}, fmt.Errorf("%w: machine_version is required", ErrInvalidVerificationPreflightAdmission)
	}

	verdict, err := EvaluateChangePlanVerificationFreshness(now, plan, evidence, policy)
	if err != nil {
		return VerificationPreflightAdmission{}, fmt.Errorf("%w: %v", ErrInvalidVerificationPreflightAdmission, err)
	}
	evidenceID, err := verificationEvidenceIdentity(evidence)
	if err != nil {
		return VerificationPreflightAdmission{}, fmt.Errorf("%w: %v", ErrInvalidVerificationPreflightAdmission, err)
	}

	admission := VerificationPreflightAdmission{
		SchemaVersion:             VerificationPreflightAdmissionSchemaVersion,
		PlanID:                    plan.PlanID,
		RevisionID:                plan.RevisionID,
		EvidenceID:                evidenceID,
		MachineVersion:            snapshot.Version,
		EvaluatedAt:               now.UTC(),
		Ready:                     verdict.Ready,
		MissingChecks:             append([]string(nil), verdict.MissingChecks...),
		StaleChecks:               append([]string(nil), verdict.StaleChecks...),
		FailedChecks:              append([]string(nil), verdict.FailedChecks...),
		ExecutionAuthorized:       false,
		ProductionMutationAllowed: false,
	}
	if admission.Ready {
		admission.ExpiresAt = verificationExpiry(evidence, policy.MaxAge)
		if !admission.ExpiresAt.After(admission.EvaluatedAt) {
			return VerificationPreflightAdmission{}, fmt.Errorf("%w: ready admission is already expired", ErrInvalidVerificationPreflightAdmission)
		}
	}
	admission.AdmissionID = verificationPreflightAdmissionIdentity(admission)
	return admission, nil
}

// Event converts a still-valid ready admission into the existing typed
// preflight_passed event. The derived event remains state-machine input only;
// it carries no adapter command, endpoint, credential or execution authority.
// expires_at is an exclusive boundary: an event at the exact expiry instant is
// rejected fail-closed.
func (admission VerificationPreflightAdmission) Event(at time.Time) (ChangeEvent, uint64, error) {
	if err := validateVerificationPreflightAdmission(admission); err != nil {
		return ChangeEvent{}, 0, err
	}
	if !admission.Ready {
		return ChangeEvent{}, 0, fmt.Errorf("%w: admission is not ready", ErrInvalidVerificationPreflightAdmission)
	}
	if at.IsZero() {
		return ChangeEvent{}, 0, fmt.Errorf("%w: event time is required", ErrInvalidVerificationPreflightAdmission)
	}
	at = at.UTC()
	if at.Before(admission.EvaluatedAt) {
		return ChangeEvent{}, 0, fmt.Errorf("%w: event time precedes admission", ErrInvalidVerificationPreflightAdmission)
	}
	if !at.Before(admission.ExpiresAt) {
		return ChangeEvent{}, 0, fmt.Errorf("%w: admission expired", ErrInvalidVerificationPreflightAdmission)
	}
	return ChangeEvent{
		ID:   "preflight-" + strings.TrimPrefix(admission.AdmissionID, "sha256:"),
		Type: EventPreflightPassed,
		At:   at,
	}, admission.MachineVersion, nil
}

// ApplyVerifiedPreflight is the safe bridge between freshness admission and
// the existing state machine. It rechecks exact plan/state/version before the
// preflight_passed transition. It does not execute networking; temporary apply
// remains a separate later state-machine event and adapter boundary.
func ApplyVerifiedPreflight(
	machine *ChangeMachine,
	admission VerificationPreflightAdmission,
	at time.Time,
) (ChangeSnapshot, error) {
	if machine == nil {
		return ChangeSnapshot{}, fmt.Errorf("%w: machine is required", ErrInvalidVerificationPreflightAdmission)
	}
	snapshot := machine.Snapshot()
	if snapshot.PlanID != admission.PlanID {
		return snapshot, fmt.Errorf("%w: machine plan_id mismatch", ErrInvalidVerificationPreflightAdmission)
	}
	if snapshot.State != ChangeStatePreflight {
		return snapshot, fmt.Errorf("%w: machine state %q is not preflight", ErrInvalidVerificationPreflightAdmission, snapshot.State)
	}
	if snapshot.Version != admission.MachineVersion {
		return snapshot, fmt.Errorf("%w: machine version changed", ErrInvalidVerificationPreflightAdmission)
	}
	event, expectedVersion, err := admission.Event(at)
	if err != nil {
		return snapshot, err
	}
	return machine.Apply(event, expectedVersion)
}

func validateVerificationPreflightAdmission(admission VerificationPreflightAdmission) error {
	if admission.SchemaVersion != VerificationPreflightAdmissionSchemaVersion {
		return fmt.Errorf("%w: unsupported schema_version %q", ErrInvalidVerificationPreflightAdmission, admission.SchemaVersion)
	}
	if err := validateDigestID("admission_id", admission.AdmissionID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidVerificationPreflightAdmission, err)
	}
	if err := validateDigestID("plan_id", admission.PlanID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidVerificationPreflightAdmission, err)
	}
	revisionID, err := normalizeIdentifier("revision_id", admission.RevisionID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidVerificationPreflightAdmission, err)
	}
	if admission.RevisionID != revisionID {
		return fmt.Errorf("%w: revision_id must be canonical", ErrInvalidVerificationPreflightAdmission)
	}
	if err := validateDigestID("evidence_id", admission.EvidenceID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidVerificationPreflightAdmission, err)
	}
	if admission.MachineVersion == 0 || admission.EvaluatedAt.IsZero() {
		return fmt.Errorf("%w: machine_version and evaluated_at are required", ErrInvalidVerificationPreflightAdmission)
	}
	if admission.ExecutionAuthorized || admission.ProductionMutationAllowed {
		return fmt.Errorf("%w: admission cannot authorize execution or production mutation", ErrInvalidVerificationPreflightAdmission)
	}
	if admission.Ready {
		if admission.ExpiresAt.IsZero() || !admission.ExpiresAt.After(admission.EvaluatedAt) {
			return fmt.Errorf("%w: ready admission requires a future expires_at", ErrInvalidVerificationPreflightAdmission)
		}
		if len(admission.MissingChecks) != 0 || len(admission.StaleChecks) != 0 || len(admission.FailedChecks) != 0 {
			return fmt.Errorf("%w: ready admission cannot contain rejection reasons", ErrInvalidVerificationPreflightAdmission)
		}
	} else if !admission.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: rejected admission must not carry expires_at", ErrInvalidVerificationPreflightAdmission)
	}
	if got := verificationPreflightAdmissionIdentity(admission); got != admission.AdmissionID {
		return fmt.Errorf("%w: admission identity mismatch", ErrInvalidVerificationPreflightAdmission)
	}
	return nil
}

func verificationExpiry(evidence VerificationEvidence, maxAge time.Duration) time.Time {
	expiresAt := evidence.VerifiedAt.UTC().Add(maxAge)
	for _, check := range evidence.Checks {
		candidate := check.ObservedAt.UTC().Add(maxAge)
		if candidate.Before(expiresAt) {
			expiresAt = candidate
		}
	}
	return expiresAt
}

func verificationEvidenceIdentity(evidence VerificationEvidence) (string, error) {
	canonical := evidence
	canonical.VerifiedAt = canonical.VerifiedAt.UTC()
	canonical.Checks = append([]VerificationCheckEvidence(nil), evidence.Checks...)
	for index := range canonical.Checks {
		canonical.Checks[index].Name = strings.TrimSpace(canonical.Checks[index].Name)
		canonical.Checks[index].ObservedAt = canonical.Checks[index].ObservedAt.UTC()
	}
	sort.Slice(canonical.Checks, func(i, j int) bool { return canonical.Checks[i].Name < canonical.Checks[j].Name })
	document, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(document)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func verificationPreflightAdmissionIdentity(admission VerificationPreflightAdmission) string {
	admission.AdmissionID = ""
	admission.EvaluatedAt = admission.EvaluatedAt.UTC()
	if !admission.ExpiresAt.IsZero() {
		admission.ExpiresAt = admission.ExpiresAt.UTC()
	}
	admission.MissingChecks = append([]string(nil), admission.MissingChecks...)
	admission.StaleChecks = append([]string(nil), admission.StaleChecks...)
	admission.FailedChecks = append([]string(nil), admission.FailedChecks...)
	sort.Strings(admission.MissingChecks)
	sort.Strings(admission.StaleChecks)
	sort.Strings(admission.FailedChecks)
	document, err := json.Marshal(admission)
	if err != nil {
		panic("verification preflight admission contains only JSON-safe values: " + err.Error())
	}
	digest := sha256.Sum256(document)
	return "sha256:" + hex.EncodeToString(digest[:])
}
