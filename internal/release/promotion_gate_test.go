package release

import "testing"

func TestEvaluatePromotionGateStable(t *testing.T) {
	decision, err := EvaluatePromotionGate("stable", PromotionEvidence{
		Version:          "1.0.0",
		TestsPassed:      true,
		SecurityPassed:   true,
		ArtifactSigned:   true,
		RollbackPrepared: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Allowed || len(decision.Blockers) != 0 {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestEvaluatePromotionGateStableReportsBlockers(t *testing.T) {
	decision, err := EvaluatePromotionGate("stable", PromotionEvidence{Version: "1.0.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"tests", "security", "signature", "rollback"}
	if len(decision.Blockers) != len(want) {
		t.Fatalf("blockers=%#v want=%#v", decision.Blockers, want)
	}
	for i := range want {
		if decision.Blockers[i] != want[i] {
			t.Fatalf("blockers=%#v want=%#v", decision.Blockers, want)
		}
	}
}

func TestEvaluatePromotionGateDevelopmentDoesNotRequireSignature(t *testing.T) {
	decision, err := EvaluatePromotionGate("development", PromotionEvidence{Version: "1.0.0-dev", TestsPassed: true, SecurityPassed: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("unexpected blockers: %#v", decision.Blockers)
	}
}
