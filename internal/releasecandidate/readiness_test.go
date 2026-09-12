package releasecandidate

import (
	"strings"
	"testing"
)

func completeSnapshot() Snapshot {
	candidateSHA := strings.Repeat("b", 40)
	digest := "sha256:" + strings.Repeat("c", 64)
	required := RequiredGates()
	gates := make([]GateEvidence, 0, len(required))
	for _, gate := range required {
		gates = append(gates, GateEvidence{
			Gate:           gate,
			Status:         GatePass,
			CandidateSHA:   candidateSHA,
			EvidenceDigest: digest,
		})
	}
	return Snapshot{
		Schema:               SchemaV1,
		StableVersion:        StableVersion,
		StableTag:            StableTag,
		StableArtifactDigest: StableArtifactDigest,
		CandidateVersion:     CandidateVersion,
		CandidateSHA:         candidateSHA,
		Gates:                gates,
	}
}

func TestEvaluateReadyOnlyWhenEveryRequiredGatePasses(t *testing.T) {
	result, err := Evaluate(completeSnapshot())
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !result.Ready {
		t.Fatalf("Ready = false, blockers = %v", result.Blockers)
	}
	if len(result.Blockers) != 0 {
		t.Fatalf("Blockers = %v, want empty", result.Blockers)
	}
}

func TestRequiredGatesReturnsDefensiveCopy(t *testing.T) {
	first := RequiredGates()
	first[0] = GateID("weakened")
	second := RequiredGates()
	if second[0] != GateManualRetryLineage {
		t.Fatalf("RequiredGates() policy mutated through caller: %v", second)
	}
}

func TestProductStableRequiredGatesReturnsDefensiveCopy(t *testing.T) {
	first := ProductStableRequiredGates()
	first[0] = GateID("weakened")
	second := ProductStableRequiredGates()
	if second[0] != GateManualRetryLineage {
		t.Fatalf("ProductStableRequiredGates() policy mutated through caller: %v", second)
	}
	for _, gate := range second {
		if gate == GateCommercialLegal {
			t.Fatal("commercial/legal gate must not be a product Stable blocker")
		}
	}
}

func TestEvaluateProductStableAllowsSeparateCommercialLaunchBlocker(t *testing.T) {
	snapshot := completeSnapshot()
	for index := range snapshot.Gates {
		if snapshot.Gates[index].Gate == GateCommercialLegal {
			snapshot.Gates[index].Status = GateBlocked
			snapshot.Gates[index].EvidenceDigest = ""
		}
	}

	commercial, err := Evaluate(snapshot)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if commercial.Ready || len(commercial.Blockers) != 1 || commercial.Blockers[0] != GateCommercialLegal {
		t.Fatalf("commercial readiness = %+v, want only commercial/legal blocker", commercial)
	}

	stable, err := EvaluateProductStable(snapshot)
	if err != nil {
		t.Fatalf("EvaluateProductStable() error = %v", err)
	}
	if !stable.Ready || len(stable.Blockers) != 0 {
		t.Fatalf("product Stable readiness = %+v, want ready", stable)
	}
}

func TestEvaluateProductStableKeepsTechnicalSafetyFailClosed(t *testing.T) {
	snapshot := completeSnapshot()
	for index := range snapshot.Gates {
		switch snapshot.Gates[index].Gate {
		case GateCommercialLegal:
			snapshot.Gates[index].Status = GateBlocked
			snapshot.Gates[index].EvidenceDigest = ""
		case GateSecurityPrivacy:
			snapshot.Gates[index].Status = GateBlocked
			snapshot.Gates[index].EvidenceDigest = ""
		}
	}

	stable, err := EvaluateProductStable(snapshot)
	if err != nil {
		t.Fatalf("EvaluateProductStable() error = %v", err)
	}
	if stable.Ready {
		t.Fatal("Ready = true, want false")
	}
	if len(stable.Blockers) != 1 || stable.Blockers[0] != GateSecurityPrivacy {
		t.Fatalf("Blockers = %v, want [%s]", stable.Blockers, GateSecurityPrivacy)
	}
}

func TestEvaluateMissingGateBlocksReadiness(t *testing.T) {
	snapshot := completeSnapshot()
	snapshot.Gates = snapshot.Gates[:len(snapshot.Gates)-1]

	result, err := Evaluate(snapshot)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.Ready {
		t.Fatal("Ready = true, want false")
	}
	if len(result.Blockers) != 1 || result.Blockers[0] != GateReleaseMetadata {
		t.Fatalf("Blockers = %v, want [%s]", result.Blockers, GateReleaseMetadata)
	}
}

func TestEvaluatePendingGateBlocksWithoutInventingEvidence(t *testing.T) {
	snapshot := completeSnapshot()
	snapshot.Gates[2].Status = GatePending
	snapshot.Gates[2].EvidenceDigest = ""

	result, err := Evaluate(snapshot)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.Ready {
		t.Fatal("Ready = true, want false")
	}
	if len(result.Blockers) != 1 || result.Blockers[0] != GatePackaging {
		t.Fatalf("Blockers = %v, want [%s]", result.Blockers, GatePackaging)
	}
}

func TestEvaluateRejectsDuplicateGate(t *testing.T) {
	snapshot := completeSnapshot()
	snapshot.Gates = append(snapshot.Gates, snapshot.Gates[0])

	if _, err := Evaluate(snapshot); err == nil {
		t.Fatal("Evaluate() error = nil, want duplicate gate error")
	}
}

func TestEvaluateRejectsUnknownGate(t *testing.T) {
	snapshot := completeSnapshot()
	snapshot.Gates[0].Gate = GateID("future_unreviewed_gate")

	if _, err := Evaluate(snapshot); err == nil {
		t.Fatal("Evaluate() error = nil, want unknown gate error")
	}
}

func TestEvaluateRejectsEvidenceForAnotherCandidate(t *testing.T) {
	snapshot := completeSnapshot()
	snapshot.Gates[0].CandidateSHA = strings.Repeat("d", 40)

	if _, err := Evaluate(snapshot); err == nil {
		t.Fatal("Evaluate() error = nil, want candidate binding error")
	}
}

func TestEvaluateRejectsPassWithoutDigest(t *testing.T) {
	snapshot := completeSnapshot()
	snapshot.Gates[0].EvidenceDigest = ""

	if _, err := Evaluate(snapshot); err == nil {
		t.Fatal("Evaluate() error = nil, want missing evidence digest error")
	}
}

func TestEvaluateRejectsUnexpectedStableIdentity(t *testing.T) {
	snapshot := completeSnapshot()
	snapshot.StableVersion = "0.29.0"

	if _, err := Evaluate(snapshot); err == nil {
		t.Fatal("Evaluate() error = nil, want stable identity error")
	}
}

func TestEvaluateRejectsDifferentStableArtifact(t *testing.T) {
	snapshot := completeSnapshot()
	snapshot.StableArtifactDigest = "sha256:" + strings.Repeat("d", 64)

	if _, err := Evaluate(snapshot); err == nil {
		t.Fatal("Evaluate() error = nil, want stable artifact binding error")
	}
}
