package networkpolicy

import (
	"strings"
	"testing"
	"time"
)

func TestBuildCurrentVerificationPreflightAdmissionBindsAuthoritativePlan(t *testing.T) {
	plan, machine, _, now := preflightFixture(t)
	evidence := planVerificationEvidence(plan, now.Add(-time.Second))
	policy := ChangePlanVerificationFreshnessPolicy{MaxAge: 20 * time.Second, MaxFutureSkew: time.Second}

	admission, err := BuildCurrentVerificationPreflightAdmission(now, plan, machine, evidence, policy)
	if err != nil {
		t.Fatalf("BuildCurrentVerificationPreflightAdmission() error = %v", err)
	}
	if !admission.Ready || admission.PlanID != plan.PlanID || admission.RevisionID != plan.RevisionID {
		t.Fatalf("admission = %#v", admission)
	}
}

func TestBuildCurrentVerificationPreflightAdmissionRejectsAuthoritativePlanDrift(t *testing.T) {
	plan, machine, _, now := preflightFixture(t)
	evidence := planVerificationEvidence(plan, now.Add(-time.Second))
	policy := ChangePlanVerificationFreshnessPolicy{MaxAge: 20 * time.Second, MaxFutureSkew: time.Second}
	currentPlan := changedAuthoritativeVerificationPlan(t)

	if _, err := BuildCurrentVerificationPreflightAdmission(now, currentPlan, machine, evidence, policy); err == nil {
		t.Fatal("drifted authoritative plan was accepted")
	}
	if snapshot := machine.Snapshot(); snapshot.State != ChangeStatePreflight {
		t.Fatalf("machine state changed after rejected authoritative drift: %#v", snapshot)
	}
}

func TestApplyCurrentVerifiedPreflightRejectsDesiredStateDrift(t *testing.T) {
	plan, machine, _, now := preflightFixture(t)
	evidence := planVerificationEvidence(plan, now.Add(-time.Second))
	policy := ChangePlanVerificationFreshnessPolicy{MaxAge: 20 * time.Second, MaxFutureSkew: time.Second}
	admission, err := BuildCurrentVerificationPreflightAdmission(now, plan, machine, evidence, policy)
	if err != nil {
		t.Fatal(err)
	}

	currentPlan := changedAuthoritativeVerificationPlan(t)
	if _, err := ApplyCurrentVerifiedPreflight(machine, currentPlan, admission, evidence, policy, now.Add(time.Second)); err == nil {
		t.Fatal("admission for superseded desired state advanced preflight")
	}
	if snapshot := machine.Snapshot(); snapshot.State != ChangeStatePreflight {
		t.Fatalf("machine state changed after rejected desired-state drift: %#v", snapshot)
	}
}

func TestApplyCurrentVerifiedPreflightRejectsEvidenceSubstitution(t *testing.T) {
	plan, machine, _, now := preflightFixture(t)
	evidence := planVerificationEvidence(plan, now.Add(-time.Second))
	policy := ChangePlanVerificationFreshnessPolicy{MaxAge: 20 * time.Second, MaxFutureSkew: time.Second}
	admission, err := BuildCurrentVerificationPreflightAdmission(now, plan, machine, evidence, policy)
	if err != nil {
		t.Fatal(err)
	}

	forged := admission
	forged.EvidenceID = "sha256:" + strings.Repeat("f", 64)
	forged.AdmissionID = verificationPreflightAdmissionIdentity(forged)

	if _, err := ApplyCurrentVerifiedPreflight(machine, plan, forged, evidence, policy, now.Add(time.Second)); err == nil {
		t.Fatal("self-consistent admission with substituted evidence_id advanced preflight")
	}
	if snapshot := machine.Snapshot(); snapshot.State != ChangeStatePreflight {
		t.Fatalf("machine state changed after rejected evidence substitution: %#v", snapshot)
	}
}

func TestApplyCurrentVerifiedPreflightRejectsForgedExpiryPastEvidenceFreshness(t *testing.T) {
	plan, machine, _, now := preflightFixture(t)
	evidence := planVerificationEvidence(plan, now.Add(-time.Second))
	policy := ChangePlanVerificationFreshnessPolicy{MaxAge: 20 * time.Second, MaxFutureSkew: time.Second}
	admission, err := BuildCurrentVerificationPreflightAdmission(now, plan, machine, evidence, policy)
	if err != nil {
		t.Fatal(err)
	}

	forged := admission
	forged.ExpiresAt = admission.ExpiresAt.Add(time.Minute)
	forged.AdmissionID = verificationPreflightAdmissionIdentity(forged)

	if _, err := ApplyCurrentVerifiedPreflight(machine, plan, forged, evidence, policy, admission.ExpiresAt); err == nil {
		t.Fatal("admission with forged extended expiry advanced after authoritative evidence expired")
	}
	if snapshot := machine.Snapshot(); snapshot.State != ChangeStatePreflight {
		t.Fatalf("machine state changed after rejected forged expiry: %#v", snapshot)
	}
}

func TestApplyCurrentVerifiedPreflightAdvancesMatchingAuthoritativePlan(t *testing.T) {
	plan, machine, _, now := preflightFixture(t)
	evidence := planVerificationEvidence(plan, now.Add(-time.Second))
	policy := ChangePlanVerificationFreshnessPolicy{MaxAge: 20 * time.Second, MaxFutureSkew: time.Second}
	admission, err := BuildCurrentVerificationPreflightAdmission(now, plan, machine, evidence, policy)
	if err != nil {
		t.Fatal(err)
	}

	updated, err := ApplyCurrentVerifiedPreflight(machine, plan, admission, evidence, policy, now.Add(time.Second))
	if err != nil {
		t.Fatalf("ApplyCurrentVerifiedPreflight() error = %v", err)
	}
	if updated.State != ChangeStateApplyWindow {
		t.Fatalf("updated state = %s, want %s", updated.State, ChangeStateApplyWindow)
	}
}

func changedAuthoritativeVerificationPlan(t *testing.T) ChangePlan {
	t.Helper()
	request := validChangePlanRequest()
	request.RevisionID = "network-revision-authoritative-next"
	plan, err := BuildChangePlan(request)
	if err != nil {
		t.Fatalf("BuildChangePlan() error = %v", err)
	}
	return plan
}
