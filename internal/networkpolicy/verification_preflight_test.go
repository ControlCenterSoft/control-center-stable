package networkpolicy

import (
	"strings"
	"testing"
	"time"
)

func TestVerificationPreflightAdmissionAdvancesOnlyFreshExactPreflight(t *testing.T) {
	plan, machine, snapshot, now := preflightFixture(t)
	evidence := planVerificationEvidence(plan, now.Add(-time.Second))
	policy := ChangePlanVerificationFreshnessPolicy{MaxAge: 20 * time.Second, MaxFutureSkew: time.Second}

	admission, err := BuildVerificationPreflightAdmission(now, plan, snapshot, evidence, policy)
	if err != nil {
		t.Fatalf("BuildVerificationPreflightAdmission() error = %v", err)
	}
	if !admission.Ready {
		t.Fatalf("admission.Ready = false, admission = %#v", admission)
	}
	if admission.ExecutionAuthorized || admission.ProductionMutationAllowed {
		t.Fatalf("admission unexpectedly grants mutation authority: %#v", admission)
	}
	if admission.PlanID != plan.PlanID || admission.RevisionID != plan.RevisionID || admission.MachineVersion != snapshot.Version {
		t.Fatalf("admission binding = %#v", admission)
	}
	if !strings.HasPrefix(admission.AdmissionID, "sha256:") || !strings.HasPrefix(admission.EvidenceID, "sha256:") {
		t.Fatalf("admission identities = %#v", admission)
	}

	updated, err := ApplyVerifiedPreflight(machine, admission, now.Add(time.Second))
	if err != nil {
		t.Fatalf("ApplyVerifiedPreflight() error = %v", err)
	}
	if updated.State != ChangeStateApplyWindow || updated.Version != snapshot.Version+1 {
		t.Fatalf("updated snapshot = %#v", updated)
	}
}

func TestVerificationPreflightAdmissionRejectsStaleFailedAndMissingEvidence(t *testing.T) {
	plan, _, snapshot, now := preflightFixture(t)
	policy := ChangePlanVerificationFreshnessPolicy{MaxAge: 10 * time.Second, MaxFutureSkew: time.Second}

	tests := []struct {
		name   string
		mutate func(*VerificationEvidence)
	}{
		{
			name: "stale",
			mutate: func(evidence *VerificationEvidence) {
				evidence.VerifiedAt = now.Add(-time.Minute)
				for index := range evidence.Checks {
					evidence.Checks[index].ObservedAt = evidence.VerifiedAt
				}
			},
		},
		{
			name: "failed",
			mutate: func(evidence *VerificationEvidence) {
				evidence.Checks[0].Status = VerificationCheckFail
			},
		},
		{
			name: "missing",
			mutate: func(evidence *VerificationEvidence) {
				evidence.Checks = evidence.Checks[:len(evidence.Checks)-1]
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := planVerificationEvidence(plan, now.Add(-time.Second))
			test.mutate(&evidence)
			admission, err := BuildVerificationPreflightAdmission(now, plan, snapshot, evidence, policy)
			if err != nil {
				t.Fatalf("BuildVerificationPreflightAdmission() error = %v", err)
			}
			if admission.Ready {
				t.Fatalf("unsafe admission became ready: %#v", admission)
			}
			if _, _, err := admission.Event(now); err == nil {
				t.Fatal("rejected admission produced preflight event")
			}
		})
	}
}

func TestVerificationPreflightAdmissionExpiresAtOldestRequiredEvidence(t *testing.T) {
	plan, _, snapshot, now := preflightFixture(t)
	evidence := planVerificationEvidence(plan, now.Add(-2*time.Second))
	evidence.Checks[0].ObservedAt = now.Add(-5 * time.Second)
	policy := ChangePlanVerificationFreshnessPolicy{MaxAge: 10 * time.Second, MaxFutureSkew: time.Second}

	admission, err := BuildVerificationPreflightAdmission(now, plan, snapshot, evidence, policy)
	if err != nil {
		t.Fatal(err)
	}
	wantExpiry := now.Add(5 * time.Second)
	if !admission.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("expires_at = %s, want %s", admission.ExpiresAt, wantExpiry)
	}
	if _, _, err := admission.Event(wantExpiry.Add(time.Nanosecond)); err == nil {
		t.Fatal("expired admission produced preflight event")
	}
}

func TestVerificationPreflightAdmissionRejectsChangedMachineAndTampering(t *testing.T) {
	plan, machine, snapshot, now := preflightFixture(t)
	evidence := planVerificationEvidence(plan, now.Add(-time.Second))
	policy := ChangePlanVerificationFreshnessPolicy{MaxAge: 20 * time.Second, MaxFutureSkew: time.Second}
	admission, err := BuildVerificationPreflightAdmission(now, plan, snapshot, evidence, policy)
	if err != nil {
		t.Fatal(err)
	}

	tampered := admission
	tampered.RevisionID = "network-revision-tampered"
	if _, _, err := tampered.Event(now.Add(time.Second)); err == nil {
		t.Fatal("tampered admission identity was accepted")
	}

	advanced, err := ApplyVerifiedPreflight(machine, admission, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if advanced.State != ChangeStateApplyWindow {
		t.Fatalf("state = %s", advanced.State)
	}
	if _, err := ApplyVerifiedPreflight(machine, admission, now.Add(2*time.Second)); err == nil {
		t.Fatal("stale machine-version admission replay was accepted")
	}
}

func TestVerificationPreflightAdmissionIdentityIgnoresEvidenceInputOrder(t *testing.T) {
	plan, _, snapshot, now := preflightFixture(t)
	firstEvidence := planVerificationEvidence(plan, now.Add(-time.Second))
	secondEvidence := planVerificationEvidence(plan, now.Add(-time.Second))
	for left, right := 0, len(secondEvidence.Checks)-1; left < right; left, right = left+1, right-1 {
		secondEvidence.Checks[left], secondEvidence.Checks[right] = secondEvidence.Checks[right], secondEvidence.Checks[left]
	}
	policy := ChangePlanVerificationFreshnessPolicy{MaxAge: 20 * time.Second, MaxFutureSkew: time.Second}

	first, err := BuildVerificationPreflightAdmission(now, plan, snapshot, firstEvidence, policy)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildVerificationPreflightAdmission(now, plan, snapshot, secondEvidence, policy)
	if err != nil {
		t.Fatal(err)
	}
	if first.EvidenceID != second.EvidenceID || first.AdmissionID != second.AdmissionID {
		t.Fatalf("canonical identity differs: first=%#v second=%#v", first, second)
	}
}

func preflightFixture(t *testing.T) (ChangePlan, *ChangeMachine, ChangeSnapshot, time.Time) {
	t.Helper()
	plan, err := BuildChangePlan(validChangePlanRequest())
	if err != nil {
		t.Fatalf("BuildChangePlan() error = %v", err)
	}
	startedAt := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	machine, err := NewChangeMachine(plan, startedAt)
	if err != nil {
		t.Fatalf("NewChangeMachine() error = %v", err)
	}
	snapshot, err := machine.Apply(ChangeEvent{
		ID:         "snapshot-captured",
		Type:       EventSnapshotCaptured,
		SnapshotID: "recovery-028",
		At:         startedAt.Add(time.Second),
	}, 1)
	if err != nil {
		t.Fatalf("snapshot transition error = %v", err)
	}
	return plan, machine, snapshot, startedAt.Add(5 * time.Second)
}

func planVerificationEvidence(plan ChangePlan, verifiedAt time.Time) VerificationEvidence {
	evidence := VerificationEvidence{
		SchemaVersion: VerificationFreshnessSchemaVersion,
		PlanID:        plan.PlanID,
		RevisionID:    plan.RevisionID,
		VerifiedAt:    verifiedAt,
		Checks:        make([]VerificationCheckEvidence, 0, len(plan.Probes)),
	}
	for index, probe := range plan.Probes {
		evidence.Checks = append(evidence.Checks, VerificationCheckEvidence{
			Name:           probe.ID,
			Status:         VerificationCheckPass,
			EvidenceDigest: digestID(string(rune('a' + index))),
			ObservedAt:     verifiedAt,
		})
	}
	return evidence
}
