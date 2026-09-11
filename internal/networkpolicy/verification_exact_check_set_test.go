package networkpolicy

import (
	"testing"
	"time"
)

func TestEvaluateChangePlanVerificationFreshnessRejectsUnexpectedCheck(t *testing.T) {
	plan, err := BuildChangePlan(validChangePlanRequest())
	if err != nil {
		t.Fatalf("BuildChangePlan() error = %v", err)
	}
	now := time.Date(2026, 9, 11, 17, 45, 0, 0, time.UTC)
	evidence := completeVerificationEvidenceForPlan(now, plan)
	evidence.Checks = append(evidence.Checks, VerificationCheckEvidence{
		Name:           "probe-from-another-plan",
		Status:         VerificationCheckPass,
		EvidenceDigest: digestID("8"),
		ObservedAt:     evidence.VerifiedAt,
	})

	verdict, err := EvaluateChangePlanVerificationFreshness(
		now,
		plan,
		evidence,
		ChangePlanVerificationFreshnessPolicy{MaxAge: 10 * time.Minute, MaxFutureSkew: 30 * time.Second},
	)
	if err == nil {
		t.Fatalf("mixed-plan evidence accepted: %#v", verdict)
	}
}
