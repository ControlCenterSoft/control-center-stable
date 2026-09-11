package networkpolicy

import (
	"fmt"
	"time"
)

// BuildCurrentVerificationPreflightAdmission is the orchestration boundary for
// turning verification evidence into a preflight admission. In addition to the
// immutable evidence checks performed by BuildVerificationPreflightAdmission,
// it requires the change machine to still represent the exact authoritative
// ChangePlan supplied by the caller. A desired-state revision change therefore
// invalidates the old machine/evidence path before an admission can be built.
func BuildCurrentVerificationPreflightAdmission(
	now time.Time,
	currentPlan ChangePlan,
	machine *ChangeMachine,
	evidence VerificationEvidence,
	policy ChangePlanVerificationFreshnessPolicy,
) (VerificationPreflightAdmission, error) {
	snapshot, err := validateCurrentVerificationPlan(machine, currentPlan)
	if err != nil {
		return VerificationPreflightAdmission{}, err
	}

	return BuildVerificationPreflightAdmission(now, currentPlan, snapshot, evidence, policy)
}

// ApplyCurrentVerifiedPreflight revalidates both the authoritative ChangePlan
// and the authoritative verification evidence at the state-transition
// boundary. A serialized admission is only a deterministic snapshot: its
// self-hash is not proof that it was produced from trusted evidence. Requiring
// the exact source evidence here prevents a caller from forging a ready
// admission, extending its expiry, or substituting an unrelated evidence ID.
//
// The admission remains non-authorizing; normal state-machine version, expiry,
// and transition checks are still enforced by ApplyVerifiedPreflight.
func ApplyCurrentVerifiedPreflight(
	machine *ChangeMachine,
	currentPlan ChangePlan,
	admission VerificationPreflightAdmission,
	evidence VerificationEvidence,
	policy ChangePlanVerificationFreshnessPolicy,
	at time.Time,
) (ChangeSnapshot, error) {
	snapshot, err := validateCurrentVerificationPlan(machine, currentPlan)
	if err != nil {
		return snapshot, err
	}
	if admission.PlanID != currentPlan.PlanID || admission.RevisionID != currentPlan.RevisionID {
		return snapshot, fmt.Errorf("%w: admission does not match current authoritative plan", ErrInvalidVerificationPreflightAdmission)
	}
	if at.IsZero() {
		return snapshot, fmt.Errorf("%w: transition time is required", ErrInvalidVerificationPreflightAdmission)
	}

	evidenceID, err := verificationEvidenceIdentity(evidence)
	if err != nil {
		return snapshot, fmt.Errorf("%w: authoritative evidence identity: %v", ErrInvalidVerificationPreflightAdmission, err)
	}
	if evidenceID != admission.EvidenceID {
		return snapshot, fmt.Errorf("%w: admission does not match current authoritative evidence", ErrInvalidVerificationPreflightAdmission)
	}

	verdict, err := EvaluateChangePlanVerificationFreshness(at, currentPlan, evidence, policy)
	if err != nil {
		return snapshot, fmt.Errorf("%w: authoritative evidence revalidation: %v", ErrInvalidVerificationPreflightAdmission, err)
	}
	if !verdict.Ready {
		return snapshot, fmt.Errorf("%w: authoritative verification evidence is no longer ready", ErrInvalidVerificationPreflightAdmission)
	}

	expectedExpiry := verificationExpiry(evidence, policy.MaxAge)
	if !admission.ExpiresAt.Equal(expectedExpiry) {
		return snapshot, fmt.Errorf("%w: admission expiry does not match authoritative evidence", ErrInvalidVerificationPreflightAdmission)
	}

	return ApplyVerifiedPreflight(machine, admission, at)
}

func validateCurrentVerificationPlan(machine *ChangeMachine, currentPlan ChangePlan) (ChangeSnapshot, error) {
	if machine == nil {
		return ChangeSnapshot{}, fmt.Errorf("%w: change machine is required", ErrInvalidVerificationPreflightAdmission)
	}

	validated, err := BuildChangePlan(ChangePlanRequest{
		NodeID:     currentPlan.NodeID,
		RevisionID: currentPlan.RevisionID,
		Interfaces: currentPlan.Interfaces,
		Forwarding: currentPlan.Forwarding,
		Probes:     currentPlan.Probes,
		Timeouts:   currentPlan.Timeouts,
	})
	if err != nil {
		return machine.Snapshot(), fmt.Errorf("%w: current authoritative plan is invalid: %v", ErrInvalidVerificationPreflightAdmission, err)
	}
	if validated.PlanID != currentPlan.PlanID || validated.RevisionID != currentPlan.RevisionID {
		return machine.Snapshot(), fmt.Errorf("%w: current authoritative plan identity is not canonical", ErrInvalidVerificationPreflightAdmission)
	}

	snapshot := machine.Snapshot()
	if snapshot.PlanID != currentPlan.PlanID {
		return snapshot, fmt.Errorf("%w: change machine no longer matches current authoritative plan", ErrInvalidVerificationPreflightAdmission)
	}
	return snapshot, nil
}
