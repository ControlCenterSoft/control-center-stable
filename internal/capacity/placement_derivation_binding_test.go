package capacity

import (
	"errors"
	"testing"
	"time"

	"control-center/internal/agent"
	"control-center/internal/corecontracts"
)

func placementDerivationBindingFixture(t *testing.T) (
	PlacementRequest,
	PlacementAdviceSnapshot,
	PlacementNodeDerivationInput,
	PlacementAdviceDerivationProvenanceSnapshot,
	time.Time,
) {
	t.Helper()
	profile := validProfile()
	telemetry := projectionDerivationTelemetry()
	derivationAt := contractNow.Add(5 * time.Minute)
	evidence, err := DeriveNodeProjection(profile, telemetry, derivationAt)
	if err != nil {
		t.Fatal(err)
	}
	request := PlacementRequest{
		ScopeID:                   "scope-derived-placement",
		RequiredRole:              corecontracts.RoleWorkerNode,
		WorkloadUnit:              WorkloadDevices,
		IncrementalWorkload:       50,
		FailureReserveNodes:       0,
		MinimumNodeReservePercent: 5,
	}
	placement, err := CapturePlacementAdviceSnapshot(request, []NodeProjection{evidence.Projection})
	if err != nil {
		t.Fatal(err)
	}
	input := PlacementNodeDerivationInput{Evidence: evidence, Profile: profile, Telemetry: telemetry}
	evaluatedAt := contractNow.Add(6 * time.Minute)
	snapshot, err := CapturePlacementAdviceDerivationProvenance(
		placement,
		[]PlacementNodeDerivationInput{input},
		evaluatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	return request, placement, input, snapshot, evaluatedAt
}

func TestPlacementDerivationProvenanceBindsExactDerivation(t *testing.T) {
	_, _, input, snapshot, _ := placementDerivationBindingFixture(t)
	if snapshot.SchemaVersion != PlacementAdviceDerivationProvenanceSchemaV1 ||
		snapshot.SnapshotID == "" || snapshot.DerivationFingerprint == "" {
		t.Fatalf("unexpected snapshot identity: %#v", snapshot)
	}
	if len(snapshot.Nodes) != 1 || snapshot.Nodes[0].NodeID != input.Evidence.Projection.NodeID ||
		snapshot.Nodes[0].DerivationID != input.Evidence.DerivationID {
		t.Fatalf("derivation binding mismatch: %#v", snapshot.Nodes)
	}
	if !snapshot.AdvisoryOnly || snapshot.ProductionMutation ||
		!snapshot.Provenance.AdvisoryOnly || snapshot.Provenance.ProductionMutation {
		t.Fatalf("derived provenance crossed advisory boundary: %#v", snapshot)
	}
}

func TestPlacementDerivationProvenanceIsDeterministic(t *testing.T) {
	_, placement, input, first, evaluatedAt := placementDerivationBindingFixture(t)
	second, err := CapturePlacementAdviceDerivationProvenance(
		placement,
		[]PlacementNodeDerivationInput{input},
		evaluatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.SnapshotID != second.SnapshotID ||
		first.DerivationFingerprint != second.DerivationFingerprint {
		t.Fatalf("same exact evidence changed provenance: %#v %#v", first, second)
	}
}

func TestPlacementDerivationProvenanceRejectsTamperedProjection(t *testing.T) {
	_, placement, input, _, evaluatedAt := placementDerivationBindingFixture(t)
	input.Evidence.Projection.SafeCapacity++
	_, err := CapturePlacementAdviceDerivationProvenance(
		placement,
		[]PlacementNodeDerivationInput{input},
		evaluatedAt,
	)
	if !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("tampered projection error = %v", err)
	}
}

func TestPlacementDerivationProvenanceRejectsProfileAndTelemetryDrift(t *testing.T) {
	_, placement, input, _, evaluatedAt := placementDerivationBindingFixture(t)

	profileDrift := input
	profileDrift.Profile.ResourceVersion = "rv-profile-drift"
	if _, err := CapturePlacementAdviceDerivationProvenance(
		placement,
		[]PlacementNodeDerivationInput{profileDrift},
		evaluatedAt,
	); !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("profile drift error = %v", err)
	}

	telemetryDrift := input
	telemetryDrift.Telemetry.Revision = "rv-telemetry-drift"
	if _, err := CapturePlacementAdviceDerivationProvenance(
		placement,
		[]PlacementNodeDerivationInput{telemetryDrift},
		evaluatedAt,
	); !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("telemetry drift error = %v", err)
	}
}

func TestPlacementDerivationProvenanceDetectsHiddenObservationDrift(t *testing.T) {
	request, placement, input, snapshot, evaluatedAt := placementDerivationBindingFixture(t)
	changed := input
	changed.Telemetry.Observations = append(
		[]agent.CapacityObservation(nil),
		input.Telemetry.Observations...,
	)
	changed.Telemetry.Observations[1].Value = 62
	currentEvidence, err := DeriveNodeProjection(
		changed.Profile,
		changed.Telemetry,
		changed.Evidence.EvaluatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if currentEvidence.Projection != input.Evidence.Projection {
		t.Fatalf("fixture no longer isolates hidden observation drift")
	}
	if currentEvidence.DerivationID == input.Evidence.DerivationID {
		t.Fatalf("changed observation did not change derivation identity")
	}
	changed.Evidence = currentEvidence
	result, err := RevalidatePlacementAdviceDerivationProvenance(
		snapshot,
		placement,
		request,
		[]PlacementNodeDerivationInput{changed},
		evaluatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != PlacementAdviceEvidenceStale ||
		result.Reason != "projection-derivation-drift" ||
		result.StaleNodeID != input.Evidence.Projection.NodeID ||
		result.RecommendationReusable ||
		result.RecommendedAction != "rebuild-node-projections-from-current-evidence" {
		t.Fatalf("hidden derivation drift was not fail-closed: %#v", result)
	}
}

func TestPlacementDerivationProvenanceExpiresTelemetry(t *testing.T) {
	request, placement, input, snapshot, _ := placementDerivationBindingFixture(t)
	result, err := RevalidatePlacementAdviceDerivationProvenance(
		snapshot,
		placement,
		request,
		[]PlacementNodeDerivationInput{input},
		contractNow.Add(16*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != PlacementAdviceEvidenceStale ||
		result.Reason != "telemetry-freshness-expired" || result.RecommendationReusable {
		t.Fatalf("expired projection telemetry remained reusable: %#v", result)
	}
}

func TestPlacementDerivationProvenanceRejectsFutureOrMissingInputs(t *testing.T) {
	_, placement, input, _, evaluatedAt := placementDerivationBindingFixture(t)
	if _, err := CapturePlacementAdviceDerivationProvenance(
		placement,
		nil,
		evaluatedAt,
	); !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("missing derivation error = %v", err)
	}
	if _, err := CapturePlacementAdviceDerivationProvenance(
		placement,
		[]PlacementNodeDerivationInput{input},
		input.Evidence.EvaluatedAt.Add(-time.Second),
	); !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("future derivation error = %v", err)
	}
}
