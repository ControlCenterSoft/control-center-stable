package networkpolicy

import (
	"testing"
	"time"
)

func TestEvaluateChangePlanVerificationFreshnessRejectsEnvelopeMutation(t *testing.T) {
	now := time.Date(2026, 9, 11, 17, 20, 0, 0, time.UTC)
	policy := ChangePlanVerificationFreshnessPolicy{
		MaxAge:        10 * time.Minute,
		MaxFutureSkew: 30 * time.Second,
	}

	mutations := []struct {
		name   string
		mutate func(*ChangePlan)
	}{
		{
			name: "schema version",
			mutate: func(plan *ChangePlan) {
				plan.SchemaVersion = "network.change.plan/v0"
			},
		},
		{
			name: "default deny",
			mutate: func(plan *ChangePlan) {
				plan.ForwardingDefaultDeny = false
			},
		},
		{
			name: "staged action",
			mutate: func(plan *ChangePlan) {
				plan.Steps[0].Action = ActionCommitOrRollback
			},
		},
		{
			name: "non canonical revision",
			mutate: func(plan *ChangePlan) {
				plan.RevisionID = " " + plan.RevisionID + " "
			},
		},
	}

	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			plan, err := BuildChangePlan(validChangePlanRequest())
			if err != nil {
				t.Fatalf("BuildChangePlan() error = %v", err)
			}
			evidence := completeVerificationEvidenceForPlan(now, plan)

			verdict, err := EvaluateChangePlanVerificationFreshness(now, plan, evidence, policy)
			if err != nil {
				t.Fatalf("baseline verification error = %v", err)
			}
			if !verdict.Ready {
				t.Fatalf("baseline verdict = %#v, want ready", verdict)
			}

			test.mutate(&plan)
			if verdict, err := EvaluateChangePlanVerificationFreshness(now, plan, evidence, policy); err == nil {
				t.Fatalf("mutated plan verdict = %#v, want integrity error", verdict)
			}
		})
	}
}

func completeVerificationEvidenceForPlan(now time.Time, plan ChangePlan) VerificationEvidence {
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
			EvidenceDigest: digestID("9"),
			ObservedAt:     verifiedAt.Add(-10 * time.Second),
		})
	}
	return evidence
}
