package capacity

import (
	"testing"

	"control-center/internal/corecontracts"
)

func TestPlacementAdviceRanksEligibleNodesDeterministically(t *testing.T) {
	request := PlacementRequest{
		ScopeID:                   "site-a",
		RequiredRole:              corecontracts.RoleWorkerNode,
		WorkloadUnit:              WorkloadDevices,
		IncrementalWorkload:       20,
		FailureReserveNodes:       1,
		MinimumNodeReservePercent: 20,
	}
	nodes := []NodeProjection{
		projection("node-c", 60, 10, 30),
		projection("node-a", 100, 40, 25),
		projection("node-b", 80, 20, 10),
	}

	advice, err := BuildPlacementAdvice(request, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if advice.SchemaVersion != PlacementAdviceSchemaV1 || advice.RecommendedNodeID != "node-c" || advice.Action != ActionNone {
		t.Fatalf("unexpected placement advice: %#v", advice)
	}
	if !advice.FleetAssessment.Safe || !advice.AdvisoryOnly || advice.ProductionMutation {
		t.Fatalf("placement advice crossed safety boundary: %#v", advice)
	}
	if len(advice.Candidates) != 3 || advice.Candidates[0].NodeID != "node-c" || !advice.Candidates[0].Eligible {
		t.Fatalf("candidate ranking is unexpected: %#v", advice.Candidates)
	}

	reversed, err := BuildPlacementAdvice(request, []NodeProjection{nodes[2], nodes[0], nodes[1]})
	if err != nil {
		t.Fatal(err)
	}
	if advice.AdviceID != reversed.AdviceID || advice.RecommendedNodeID != reversed.RecommendedNodeID {
		t.Fatalf("placement depends on input order: %#v %#v", advice, reversed)
	}
	for i := range advice.Candidates {
		if advice.Candidates[i] != reversed.Candidates[i] {
			t.Fatalf("candidate %d depends on input order: %#v %#v", i, advice.Candidates[i], reversed.Candidates[i])
		}
	}
}

func TestPlacementAdviceBlocksRecommendationWhenFleetReserveIsUnsafe(t *testing.T) {
	request := PlacementRequest{
		ScopeID:                   "site-a",
		RequiredRole:              corecontracts.RoleWorkerNode,
		WorkloadUnit:              WorkloadDevices,
		IncrementalWorkload:       40,
		FailureReserveNodes:       1,
		MinimumNodeReservePercent: 0,
	}
	nodes := []NodeProjection{
		projection("node-a", 50, 40, 20),
		projection("node-b", 50, 40, 20),
		projection("node-c", 50, 40, 20),
	}
	advice, err := BuildPlacementAdvice(request, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if advice.FleetAssessment.Safe || advice.RecommendedNodeID != "" || advice.Action != ActionAddRoleCapacity {
		t.Fatalf("unsafe fleet produced placement recommendation: %#v", advice)
	}
}

func TestPlacementAdviceRequestsEvidenceForLowConfidenceCandidates(t *testing.T) {
	request := PlacementRequest{
		ScopeID:                   "site-a",
		RequiredRole:              corecontracts.RoleWorkerNode,
		WorkloadUnit:              WorkloadDevices,
		IncrementalWorkload:       10,
		MinimumNodeReservePercent: 10,
	}
	node := projection("node-a", 100, 10, 50)
	node.Confidence = Confidence{Level: ConfidenceLow, Score: .4}
	advice, err := BuildPlacementAdvice(request, []NodeProjection{node})
	if err != nil {
		t.Fatal(err)
	}
	if advice.RecommendedNodeID != "" || advice.Action != ActionCollectEvidence || advice.Candidates[0].Reason != "insufficient-confidence" {
		t.Fatalf("low-confidence candidate was not held: %#v", advice)
	}
}
