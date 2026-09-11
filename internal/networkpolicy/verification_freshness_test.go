package networkpolicy

import (
	"strings"
	"testing"
	"time"
)

func TestEvaluateVerificationFreshnessReady(t *testing.T) {
	now := time.Date(2026, 9, 11, 7, 10, 0, 0, time.UTC)
	evidence := validVerificationEvidence(now)
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
	if !verdict.Ready {
		t.Fatalf("verdict.Ready = false, verdict = %#v", verdict)
	}
}

func TestEvaluateChangePlanVerificationFreshnessRequiresEveryPlanProbe(t *testing.T) {
	plan, err := BuildChangePlan(validChangePlanRequest())
	if err != nil {
		t.Fatalf("BuildChangePlan() error = %v", err)
	}
	now := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	verifiedAt := now.Add(-time.Minute)
	evidence := VerificationEvidence{
		SchemaVersion: VerificationFreshnessSchemaVersion,
		PlanID:        plan.PlanID,
		RevisionID:    plan.RevisionID,
		VerifiedAt:    verifiedAt,
		Checks:        make([]VerificationCheckEvidence, 0, len(plan.Probes)),
	}
	for _, probe := range plan.Probes {
		evidence.Checks = append(evidence.Checks, VerificationCheckEvidence{
			Name:           probe.ID,
			Status:         VerificationCheckPass,
			EvidenceDigest: digestID("e"),
			ObservedAt:     verifiedAt.Add(-10 * time.Second),
		})
	}
	policy := ChangePlanVerificationFreshnessPolicy{
		MaxAge:        10 * time.Minute,
		MaxFutureSkew: 30 * time.Second,
	}

	verdict, err := EvaluateChangePlanVerificationFreshness(now, plan, evidence, policy)
	if err != nil {
		t.Fatalf("EvaluateChangePlanVerificationFreshness() error = %v", err)
	}
	if !verdict.Ready {
		t.Fatalf("verdict.Ready = false, verdict = %#v", verdict)
	}

	missingProbe := plan.Probes[len(plan.Probes)-1].ID
	evidence.Checks = evidence.Checks[:len(evidence.Checks)-1]
	verdict, err = EvaluateChangePlanVerificationFreshness(now, plan, evidence, policy)
	if err != nil {
		t.Fatalf("missing probe returned error: %v", err)
	}
	if verdict.Ready || !containsString(verdict.MissingChecks, missingProbe) {
		t.Fatalf("missing probe verdict = %#v, want missing %q", verdict, missingProbe)
	}
}

func TestEvaluateChangePlanVerificationFreshnessRejectsMutatedPlanIdentity(t *testing.T) {
	plan, err := BuildChangePlan(validChangePlanRequest())
	if err != nil {
		t.Fatalf("BuildChangePlan() error = %v", err)
	}
	now := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	evidence := VerificationEvidence{
		SchemaVersion: VerificationFreshnessSchemaVersion,
		PlanID:        plan.PlanID,
		RevisionID:    plan.RevisionID,
		VerifiedAt:    now.Add(-time.Minute),
		Checks: []VerificationCheckEvidence{
			{
				Name:           plan.Probes[0].ID,
				Status:         VerificationCheckPass,
				EvidenceDigest: digestID("f"),
				ObservedAt:     now.Add(-time.Minute),
			},
		},
	}

	plan.RevisionID = "network-revision-tampered"
	_, err = EvaluateChangePlanVerificationFreshness(
		now,
		plan,
		evidence,
		ChangePlanVerificationFreshnessPolicy{MaxAge: 10 * time.Minute, MaxFutureSkew: 30 * time.Second},
	)
	if err == nil {
		t.Fatal("mutated plan carrying an old plan_id must be rejected")
	}
}

func TestVerificationFreshnessBoundMatchesChangePlanProbeBound(t *testing.T) {
	if maxVerificationChecks != maxProbesPerPlan {
		t.Fatalf("verification bound = %d, change plan probe bound = %d", maxVerificationChecks, maxProbesPerPlan)
	}
}

func TestEvaluateVerificationFreshnessFailsClosed(t *testing.T) {
	now := time.Date(2026, 9, 11, 7, 10, 0, 0, time.UTC)
	tests := []struct {
		name        string
		mutate      func(*VerificationEvidence, *VerificationFreshnessPolicy)
		wantError   bool
		wantReady   bool
		wantMissing string
		wantStale   string
		wantFailed  string
	}{
		{
			name: "plan mismatch",
			mutate: func(evidence *VerificationEvidence, _ *VerificationFreshnessPolicy) {
				evidence.PlanID = digestID("b")
			},
			wantError: true,
		},
		{
			name: "future verification",
			mutate: func(evidence *VerificationEvidence, _ *VerificationFreshnessPolicy) {
				evidence.VerifiedAt = now.Add(2 * time.Minute)
			},
			wantError: true,
		},
		{
			name: "stale verification and check",
			mutate: func(evidence *VerificationEvidence, _ *VerificationFreshnessPolicy) {
				evidence.VerifiedAt = now.Add(-20 * time.Minute)
				evidence.Checks[0].ObservedAt = evidence.VerifiedAt
				evidence.Checks[1].ObservedAt = evidence.VerifiedAt
			},
			wantStale: "verification",
		},
		{
			name: "missing required check",
			mutate: func(evidence *VerificationEvidence, _ *VerificationFreshnessPolicy) {
				evidence.Checks = evidence.Checks[:1]
			},
			wantMissing: "link_state",
		},
		{
			name: "failed required check",
			mutate: func(evidence *VerificationEvidence, _ *VerificationFreshnessPolicy) {
				evidence.Checks[0].Status = VerificationCheckFail
			},
			wantFailed: "control_plane",
		},
		{
			name: "duplicate evidence",
			mutate: func(evidence *VerificationEvidence, _ *VerificationFreshnessPolicy) {
				evidence.Checks = append(evidence.Checks, evidence.Checks[0])
			},
			wantError: true,
		},
		{
			name: "malformed digest",
			mutate: func(evidence *VerificationEvidence, _ *VerificationFreshnessPolicy) {
				evidence.Checks[0].EvidenceDigest = "sha256:not-a-digest"
			},
			wantError: true,
		},
		{
			name: "check newer than envelope",
			mutate: func(evidence *VerificationEvidence, _ *VerificationFreshnessPolicy) {
				evidence.Checks[0].ObservedAt = evidence.VerifiedAt.Add(2 * time.Minute)
			},
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := validVerificationEvidence(now)
			policy := VerificationFreshnessPolicy{
				MaxAge:         10 * time.Minute,
				MaxFutureSkew:  30 * time.Second,
				RequiredChecks: []string{"control_plane", "link_state"},
			}
			expectedPlanID := evidence.PlanID
			test.mutate(&evidence, &policy)
			verdict, err := EvaluateVerificationFreshness(now, expectedPlanID, "rev-028", evidence, policy)
			if test.wantError {
				if err == nil {
					t.Fatalf("expected error, verdict = %#v", verdict)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if verdict.Ready != test.wantReady {
				t.Fatalf("verdict.Ready = %v, want %v; verdict = %#v", verdict.Ready, test.wantReady, verdict)
			}
			if test.wantMissing != "" && !containsString(verdict.MissingChecks, test.wantMissing) {
				t.Fatalf("missing checks = %v, want %q", verdict.MissingChecks, test.wantMissing)
			}
			if test.wantStale != "" && !containsString(verdict.StaleChecks, test.wantStale) {
				t.Fatalf("stale checks = %v, want %q", verdict.StaleChecks, test.wantStale)
			}
			if test.wantFailed != "" && !containsString(verdict.FailedChecks, test.wantFailed) {
				t.Fatalf("failed checks = %v, want %q", verdict.FailedChecks, test.wantFailed)
			}
		})
	}
}

func validVerificationEvidence(now time.Time) VerificationEvidence {
	verifiedAt := now.Add(-time.Minute)
	return VerificationEvidence{
		SchemaVersion: VerificationFreshnessSchemaVersion,
		PlanID:        digestID("a"),
		RevisionID:    "rev-028",
		VerifiedAt:    verifiedAt,
		Checks: []VerificationCheckEvidence{
			{
				Name:           "control_plane",
				Status:         VerificationCheckPass,
				EvidenceDigest: digestID("c"),
				ObservedAt:     verifiedAt.Add(-10 * time.Second),
			},
			{
				Name:           "link_state",
				Status:         VerificationCheckPass,
				EvidenceDigest: digestID("d"),
				ObservedAt:     verifiedAt.Add(-20 * time.Second),
			},
		},
	}
}

func digestID(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
