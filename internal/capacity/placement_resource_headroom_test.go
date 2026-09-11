package capacity

import (
	"errors"
	"math"
	"testing"
	"time"

	"control-center/internal/agent"
)

func placementResourceHeadroomFixture(t *testing.T) (
	PlacementRequest,
	PlacementAdviceDerivedEvidenceSnapshot,
	PlacementNodeDerivationInput,
) {
	t.Helper()
	request, _, input, _, evaluatedAt := placementDerivationBindingFixture(t)
	derived, err := CapturePlacementAdviceFromDerivations(
		request,
		[]PlacementNodeDerivationInput{input},
		evaluatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	return request, derived, input
}

func TestPlacementResourceHeadroomExpandsQualifiedConstraints(t *testing.T) {
	request, derived, input := placementResourceHeadroomFixture(t)
	evaluatedAt := derived.DerivationProvenance.Provenance.EvaluatedAt
	got, err := BuildPlacementResourceHeadroomEnvelope(
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		evaluatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != PlacementResourceHeadroomSchemaV1 || got.EnvelopeID == "" ||
		!got.AdvisoryOnly || got.PlacementAuthorized || got.ProductionMutation {
		t.Fatalf("unsafe or incomplete resource headroom envelope: %#v", got)
	}
	if got.DerivedSnapshotID != derived.SnapshotID ||
		got.PlacementSnapshotID != derived.Placement.SnapshotID ||
		got.AdviceID != derived.Placement.Advice.AdviceID {
		t.Fatalf("derived placement lineage mismatch: %#v", got)
	}
	if len(got.Candidates) != 1 || len(got.Candidates[0].Resources) != 2 {
		t.Fatalf("resource vector = %#v", got.Candidates)
	}
	candidate := got.Candidates[0]
	if candidate.Resources[0].ConstraintID != "cpu-pressure" ||
		candidate.Resources[1].ConstraintID != "storage-pressure" {
		t.Fatalf("resource ordering is not deterministic: %#v", candidate.Resources)
	}
	if candidate.LimitingConstraintID != "storage-pressure" ||
		candidate.LimitingMetric != agent.MetricStorageUsed ||
		candidate.LimitingTargetID != "disk-001" ||
		candidate.LimitingDimension != PlacementLimitResource ||
		candidate.ScoreBand != PlacementSafetyHeadroom {
		t.Fatalf("unexpected limiting resource: %#v", candidate)
	}
	wantReserve := (700.0 - 650.0) / 700.0 * 100
	if math.Abs(candidate.LimitingResourceReservePercent-wantReserve) > 1e-9 ||
		math.Abs(candidate.EffectiveSafetyMarginPercent-wantReserve) > 1e-9 {
		t.Fatalf("effective safety margin = %#v", candidate)
	}
	if candidate.Resources[0].ObservationAgeSeconds != 7*60 ||
		candidate.Resources[1].ObservationAgeSeconds != 8*60 {
		t.Fatalf("observation ages = %#v", candidate.Resources)
	}
}

func TestPlacementResourceHeadroomIsDeterministicAndTamperEvident(t *testing.T) {
	request, derived, input := placementResourceHeadroomFixture(t)
	evaluatedAt := derived.DerivationProvenance.Provenance.EvaluatedAt
	first, err := BuildPlacementResourceHeadroomEnvelope(
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		evaluatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildPlacementResourceHeadroomEnvelope(
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		evaluatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.EnvelopeID != second.EnvelopeID {
		t.Fatalf("same exact evidence changed envelope identity: %q != %q", first.EnvelopeID, second.EnvelopeID)
	}
	if err := ValidatePlacementResourceHeadroomEnvelope(
		first,
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		evaluatedAt,
	); err != nil {
		t.Fatal(err)
	}
	tampered := first
	tampered.PlacementAuthorized = true
	if err := ValidatePlacementResourceHeadroomEnvelope(
		tampered,
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		evaluatedAt,
	); !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("tampered authority error = %v", err)
	}
}

func TestPlacementResourceHeadroomFailsClosedOnDerivedEvidenceDrift(t *testing.T) {
	request, derived, input := placementResourceHeadroomFixture(t)
	evaluatedAt := derived.DerivationProvenance.Provenance.EvaluatedAt
	changed := input
	changed.Telemetry.Observations = append(
		[]agent.CapacityObservation(nil),
		input.Telemetry.Observations...,
	)
	changed.Telemetry.Observations[0].Value = 640
	currentEvidence, err := DeriveNodeProjection(
		changed.Profile,
		changed.Telemetry,
		changed.Evidence.EvaluatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	changed.Evidence = currentEvidence
	if _, err := BuildPlacementResourceHeadroomEnvelope(
		request,
		derived,
		[]PlacementNodeDerivationInput{changed},
		evaluatedAt,
	); !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("stale derived placement error = %v", err)
	}
}

func TestPlacementResourceHeadroomBoundaryAndOverload(t *testing.T) {
	request, _, input := placementResourceHeadroomFixture(t)
	evaluatedAt := contractNow.Add(6 * time.Minute)

	for _, test := range []struct {
		name      string
		value     float64
		wantBand  PlacementSafetyScoreBand
		wantLimit float64
	}{
		{name: "at safe boundary", value: 700, wantBand: PlacementSafetyBoundary, wantLimit: 0},
		{name: "above safe boundary", value: 720, wantBand: PlacementSafetyBlocked, wantLimit: (700.0 - 720.0) / 700.0 * 100},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := input
			changed.Telemetry.Observations = append(
				[]agent.CapacityObservation(nil),
				input.Telemetry.Observations...,
			)
			for index := range changed.Telemetry.Observations {
				if changed.Telemetry.Observations[index].Metric == agent.MetricStorageUsed {
					changed.Telemetry.Observations[index].Value = test.value
				}
			}
			evidence, err := DeriveNodeProjection(
				changed.Profile,
				changed.Telemetry,
				changed.Evidence.EvaluatedAt,
			)
			if err != nil {
				t.Fatal(err)
			}
			changed.Evidence = evidence
			derived, err := CapturePlacementAdviceFromDerivations(
				request,
				[]PlacementNodeDerivationInput{changed},
				evaluatedAt,
			)
			if err != nil {
				t.Fatal(err)
			}
			got, err := BuildPlacementResourceHeadroomEnvelope(
				request,
				derived,
				[]PlacementNodeDerivationInput{changed},
				evaluatedAt,
			)
			if err != nil {
				t.Fatal(err)
			}
			candidate := got.Candidates[0]
			if candidate.ScoreBand != test.wantBand ||
				math.Abs(candidate.LimitingResourceReservePercent-test.wantLimit) > 1e-9 {
				t.Fatalf("candidate safety band = %#v", candidate)
			}
			storage := candidate.Resources[1]
			if storage.ScoreBand != test.wantBand {
				t.Fatalf("storage safety band = %#v", storage)
			}
		})
	}
}

func TestPlacementResourceHeadroomEffectiveMarginNeverExceedsInputs(t *testing.T) {
	request, _, input := placementResourceHeadroomFixture(t)
	evaluatedAt := contractNow.Add(6 * time.Minute)
	for _, storageValue := range []float64{0, 300, 650, 700} {
		changed := input
		changed.Telemetry.Observations = append(
			[]agent.CapacityObservation(nil),
			input.Telemetry.Observations...,
		)
		for index := range changed.Telemetry.Observations {
			if changed.Telemetry.Observations[index].Metric == agent.MetricStorageUsed {
				changed.Telemetry.Observations[index].Value = storageValue
			}
		}
		evidence, err := DeriveNodeProjection(
			changed.Profile,
			changed.Telemetry,
			changed.Evidence.EvaluatedAt,
		)
		if err != nil {
			t.Fatal(err)
		}
		changed.Evidence = evidence
		derived, err := CapturePlacementAdviceFromDerivations(
			request,
			[]PlacementNodeDerivationInput{changed},
			evaluatedAt,
		)
		if err != nil {
			t.Fatal(err)
		}
		got, err := BuildPlacementResourceHeadroomEnvelope(
			request,
			derived,
			[]PlacementNodeDerivationInput{changed},
			evaluatedAt,
		)
		if err != nil {
			t.Fatal(err)
		}
		candidate := got.Candidates[0]
		if candidate.EffectiveSafetyMarginPercent > candidate.WorkloadMarginAboveMinimumPercent+1e-9 ||
			candidate.EffectiveSafetyMarginPercent > candidate.LimitingResourceReservePercent+1e-9 {
			t.Fatalf("effective margin exceeds an input bound: %#v", candidate)
		}
	}
}
