package networkpolicy

import (
	"testing"
	"time"
)

func TestEvaluateVerificationFreshnessTreatsExactMaxAgeAsStale(t *testing.T) {
	now := time.Date(2026, 9, 11, 17, 30, 0, 0, time.UTC)
	evidence := validVerificationEvidence(now)
	evidence.VerifiedAt = now.Add(-10 * time.Minute)
	for index := range evidence.Checks {
		evidence.Checks[index].ObservedAt = evidence.VerifiedAt
	}

	verdict, err := EvaluateVerificationFreshness(
		now,
		evidence.PlanID,
		evidence.RevisionID,
		evidence,
		VerificationFreshnessPolicy{
			MaxAge:         10 * time.Minute,
			MaxFutureSkew:  30 * time.Second,
			RequiredChecks: []string{"control_plane", "link_state"},
		},
	)
	if err != nil {
		t.Fatalf("EvaluateVerificationFreshness() error = %v", err)
	}
	if verdict.Ready {
		t.Fatalf("exact-expiry evidence became ready: %#v", verdict)
	}
	for _, want := range []string{"verification", "control_plane", "link_state"} {
		if !containsString(verdict.StaleChecks, want) {
			t.Fatalf("stale_checks = %v, want %q", verdict.StaleChecks, want)
		}
	}
}

func TestVerificationPreflightAdmissionRejectsEventAtExactExpiry(t *testing.T) {
	plan, _, snapshot, now := preflightFixture(t)
	evidence := planVerificationEvidence(plan, now.Add(-time.Second))
	policy := ChangePlanVerificationFreshnessPolicy{
		MaxAge:        20 * time.Second,
		MaxFutureSkew: time.Second,
	}

	admission, err := BuildVerificationPreflightAdmission(now, plan, snapshot, evidence, policy)
	if err != nil {
		t.Fatalf("BuildVerificationPreflightAdmission() error = %v", err)
	}
	if !admission.Ready {
		t.Fatalf("admission = %#v, want ready before expiry", admission)
	}
	if _, _, err := admission.Event(admission.ExpiresAt); err == nil {
		t.Fatal("admission produced preflight event at exact expiry boundary")
	}
}
