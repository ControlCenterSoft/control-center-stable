package capacity

import (
	"errors"
	"reflect"
	"testing"

	"control-center/internal/corecontracts"
)

func TestPlacementSafetyMarginExplainsRecommendedNodeDeterministically(t *testing.T) {
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
	envelope, err := BuildPlacementSafetyMarginEnvelope(request, nodes, advice)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.RecommendedNodeID != "node-c" || !envelope.FleetSafe || envelope.Action != ActionNone {
		t.Fatalf("unexpected safety envelope: %#v", envelope)
	}
	if !envelope.AdvisoryOnly || envelope.PlacementAuthorized || envelope.ProductionMutation {
		t.Fatalf("safety envelope crossed advisory boundary: %#v", envelope)
	}
	if len(envelope.Candidates) != 3 {
		t.Fatalf("candidate count = %d", len(envelope.Candidates))
	}
	candidate := envelope.Candidates[0]
	if candidate.NodeID != "node-c" || candidate.WorkloadSafeReserve != 30 ||
		candidate.WorkloadSafeReservePercent != 50 || candidate.MinimumRequiredReservePercent != 20 ||
		candidate.WorkloadMarginAboveMinimumPercent != 30 || candidate.BottleneckSafeReservePercent != 30 ||
		candidate.EffectiveSafetyMarginPercent != 30 || candidate.LimitingDimension != PlacementLimitWorkload ||
		candidate.ScoreBand != PlacementSafetyHeadroom {
		t.Fatalf("recommended candidate margin is unexpected: %#v", candidate)
	}

	for range 100 {
		reversed := []NodeProjection{nodes[2], nodes[0], nodes[1]}
		reversedAdvice, buildErr := BuildPlacementAdvice(request, reversed)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		repeated, buildErr := BuildPlacementSafetyMarginEnvelope(request, reversed, reversedAdvice)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		if !reflect.DeepEqual(envelope, repeated) {
			t.Fatalf("safety envelope depends on input order: %#v %#v", envelope, repeated)
		}
	}
}

func TestPlacementSafetyMarginReportsBoundaryAndConfidenceBlock(t *testing.T) {
	request := PlacementRequest{
		ScopeID:                   "site-a",
		RequiredRole:              corecontracts.RoleWorkerNode,
		WorkloadUnit:              WorkloadDevices,
		IncrementalWorkload:       20,
		MinimumNodeReservePercent: 20,
	}
	node := projection("node-a", 100, 60, 25)
	advice, err := BuildPlacementAdvice(request, []NodeProjection{node})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := BuildPlacementSafetyMarginEnvelope(request, []NodeProjection{node}, advice)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Candidates[0].ScoreBand != PlacementSafetyBoundary ||
		envelope.Candidates[0].EffectiveSafetyMarginPercent != 0 {
		t.Fatalf("minimum-reserve boundary not explained: %#v", envelope.Candidates[0])
	}

	node = projection("node-a", 100, 10, 50)
	node.Confidence = Confidence{Level: ConfidenceLow, Score: .4}
	advice, err = BuildPlacementAdvice(request, []NodeProjection{node})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err = BuildPlacementSafetyMarginEnvelope(request, []NodeProjection{node}, advice)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Candidates[0].ScoreBand != PlacementSafetyBlocked ||
		envelope.Candidates[0].Reason != "insufficient-confidence" || envelope.RecommendedNodeID != "" {
		t.Fatalf("confidence block not preserved: %#v", envelope)
	}
}

func TestPlacementSafetyMarginUsesBottleneckAsLimitingDimension(t *testing.T) {
	request := PlacementRequest{
		ScopeID:                   "site-a",
		RequiredRole:              corecontracts.RoleWorkerNode,
		WorkloadUnit:              WorkloadDevices,
		IncrementalWorkload:       10,
		MinimumNodeReservePercent: 10,
	}
	node := projection("node-a", 100, 20, 15)
	advice, err := BuildPlacementAdvice(request, []NodeProjection{node})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := BuildPlacementSafetyMarginEnvelope(request, []NodeProjection{node}, advice)
	if err != nil {
		t.Fatal(err)
	}
	candidate := envelope.Candidates[0]
	if candidate.LimitingDimension != PlacementLimitBottleneck || candidate.EffectiveSafetyMarginPercent != 15 {
		t.Fatalf("bottleneck margin not selected: %#v", candidate)
	}
}

func TestPlacementSafetyMarginRejectsTamperedAdviceAndEnvelope(t *testing.T) {
	request := PlacementRequest{
		ScopeID:                   "site-a",
		RequiredRole:              corecontracts.RoleWorkerNode,
		WorkloadUnit:              WorkloadDevices,
		IncrementalWorkload:       10,
		MinimumNodeReservePercent: 10,
	}
	nodes := []NodeProjection{projection("node-a", 100, 20, 30)}
	advice, err := BuildPlacementAdvice(request, nodes)
	if err != nil {
		t.Fatal(err)
	}
	tampered := advice
	tampered.Candidates = append([]PlacementCandidate(nil), advice.Candidates...)
	tampered.Candidates[0].SafeReserve++
	if _, err = BuildPlacementSafetyMarginEnvelope(request, nodes, tampered); !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("tampered advice error = %v", err)
	}

	envelope, err := BuildPlacementSafetyMarginEnvelope(request, nodes, advice)
	if err != nil {
		t.Fatal(err)
	}
	envelope.PlacementAuthorized = true
	if err = ValidatePlacementSafetyMarginEnvelope(envelope, request, nodes, advice); !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("authority tamper error = %v", err)
	}
}
