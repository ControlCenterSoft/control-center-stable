package capacity

import (
	"errors"
	"testing"
	"time"

	"control-center/internal/agent"
)

func placementResourceHeadroomFreshnessFixture(t *testing.T) (
	PlacementRequest,
	PlacementAdviceDerivedEvidenceSnapshot,
	PlacementNodeDerivationInput,
	PlacementResourceHeadroomEnvelope,
	time.Time,
) {
	t.Helper()
	request, derived, input := placementResourceHeadroomFixture(t)
	evaluatedAt := derived.DerivationProvenance.Provenance.EvaluatedAt
	envelope, err := BuildPlacementResourceHeadroomEnvelope(
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		evaluatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	return request, derived, input, envelope, evaluatedAt
}

func TestPlacementResourceHeadroomFreshnessGateAllowsExactCurrentMeasuredEvidence(t *testing.T) {
	request, derived, input, envelope, evaluatedAt := placementResourceHeadroomFreshnessFixture(t)
	checkedAt := evaluatedAt.Add(10 * time.Minute)
	got, err := BuildPlacementResourceHeadroomFreshnessGate(
		envelope,
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		checkedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != PlacementResourceHeadroomFreshnessSchemaV1 || got.GateID == "" ||
		!got.AllCurrent || !got.AdviceReusePermitted || got.RecommendedAction != "none" ||
		!got.AdvisoryOnly || got.PlacementAuthorized || got.ProductionMutation {
		t.Fatalf("unexpected freshness gate: %#v", got)
	}
	if got.HeadroomEnvelopeID != envelope.EnvelopeID || got.DerivedSnapshotID != envelope.DerivedSnapshotID ||
		got.PlacementSnapshotID != envelope.PlacementSnapshotID || got.AdviceID != envelope.AdviceID {
		t.Fatalf("headroom lineage mismatch: %#v", got)
	}
	if len(got.Candidates) != 1 {
		t.Fatalf("candidate count = %d", len(got.Candidates))
	}
	candidate := got.Candidates[0]
	if candidate.Status != PlacementResourceEvidenceCurrent || !candidate.AdviceReusable ||
		candidate.RecommendedAction != "none" || len(candidate.StaleConstraintIDs) != 0 ||
		len(candidate.NonMeasuredConstraintIDs) != 0 {
		t.Fatalf("current evidence was not reusable: %#v", candidate)
	}
	if candidate.OldestObservationAgeSeconds != 18*60 ||
		candidate.MinimumFreshnessRemainingSec != 42*60 {
		t.Fatalf("freshness budget mismatch: %#v", candidate)
	}
}

func TestPlacementResourceHeadroomFreshnessGateDegradesNonMeasuredEvidence(t *testing.T) {
	request, _, input, _, evaluatedAt := placementResourceHeadroomFreshnessFixture(t)
	changed := input
	changed.Telemetry.Observations = append(
		[]agent.CapacityObservation(nil),
		input.Telemetry.Observations...,
	)
	for index := range changed.Telemetry.Observations {
		if changed.Telemetry.Observations[index].Metric == agent.MetricStorageUsed {
			changed.Telemetry.Observations[index].Evidence = agent.EvidenceBenchmark
		}
	}
	evidence, err := DeriveNodeProjection(changed.Profile, changed.Telemetry, changed.Evidence.EvaluatedAt)
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
	envelope, err := BuildPlacementResourceHeadroomEnvelope(
		request,
		derived,
		[]PlacementNodeDerivationInput{changed},
		evaluatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := BuildPlacementResourceHeadroomFreshnessGate(
		envelope,
		request,
		derived,
		[]PlacementNodeDerivationInput{changed},
		evaluatedAt.Add(10*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	candidate := got.Candidates[0]
	if got.AllCurrent || got.AdviceReusePermitted || got.RecommendedAction != "collect-evidence" ||
		candidate.Status != PlacementResourceEvidenceDegraded || candidate.AdviceReusable ||
		candidate.Reason != "non-measured-constraint-evidence" ||
		candidate.RecommendedAction != "collect-measured-evidence" ||
		len(candidate.NonMeasuredConstraintIDs) != 1 ||
		candidate.NonMeasuredConstraintIDs[0] != "storage-pressure" {
		t.Fatalf("non-measured evidence was not degraded: %#v", got)
	}
}

func TestPlacementResourceHeadroomFreshnessGateExpiresPerConstraintBudget(t *testing.T) {
	request, derived, input, envelope, _ := placementResourceHeadroomFreshnessFixture(t)
	checkedAt := contractNow.Add(59*time.Minute + time.Second)
	got, err := BuildPlacementResourceHeadroomFreshnessGate(
		envelope,
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		checkedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	candidate := got.Candidates[0]
	if candidate.Status != PlacementResourceEvidenceStale || candidate.AdviceReusable ||
		candidate.Reason != "constraint-observation-stale" ||
		candidate.RecommendedAction != "collect-current-telemetry" ||
		len(candidate.StaleConstraintIDs) != 2 ||
		candidate.StaleConstraintIDs[0] != "cpu-pressure" ||
		candidate.StaleConstraintIDs[1] != "storage-pressure" ||
		candidate.MinimumFreshnessRemainingSec >= 0 {
		t.Fatalf("stale constraint evidence remained reusable: %#v", candidate)
	}
}

func TestPlacementResourceHeadroomFreshnessGateIsDeterministicAndTamperEvident(t *testing.T) {
	request, derived, input, envelope, evaluatedAt := placementResourceHeadroomFreshnessFixture(t)
	checkedAt := evaluatedAt.Add(10 * time.Minute)
	first, err := BuildPlacementResourceHeadroomFreshnessGate(
		envelope,
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		checkedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildPlacementResourceHeadroomFreshnessGate(
		envelope,
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		checkedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.GateID != second.GateID {
		t.Fatalf("same exact evidence changed gate identity: %q != %q", first.GateID, second.GateID)
	}
	if err := ValidatePlacementResourceHeadroomFreshnessGate(
		first,
		envelope,
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		checkedAt,
	); err != nil {
		t.Fatal(err)
	}
	tampered := first
	tampered.PlacementAuthorized = true
	if err := ValidatePlacementResourceHeadroomFreshnessGate(
		tampered,
		envelope,
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		checkedAt,
	); !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("tampered authority error = %v", err)
	}
}

func TestPlacementResourceHeadroomFreshnessGateRejectsFutureCheck(t *testing.T) {
	request, derived, input, envelope, evaluatedAt := placementResourceHeadroomFreshnessFixture(t)
	_, err := BuildPlacementResourceHeadroomFreshnessGate(
		envelope,
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		evaluatedAt.Add(-time.Second),
	)
	if !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("future check error = %v", err)
	}
}

func TestPlacementResourceHeadroomFreshnessReuseImpliesMeasuredFreshConstraints(t *testing.T) {
	request, derived, input, envelope, evaluatedAt := placementResourceHeadroomFreshnessFixture(t)
	constraintByID := make(map[string]Constraint, len(input.Profile.Constraints))
	for _, constraint := range input.Profile.Constraints {
		constraintByID[constraint.ID] = constraint
	}
	for _, offset := range []time.Duration{0, 10 * time.Minute, 30 * time.Minute, 59 * time.Minute} {
		checkedAt := evaluatedAt.Add(offset)
		got, err := BuildPlacementResourceHeadroomFreshnessGate(
			envelope,
			request,
			derived,
			[]PlacementNodeDerivationInput{input},
			checkedAt,
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range got.Candidates {
			if !candidate.AdviceReusable {
				continue
			}
			for _, resource := range envelope.Candidates[0].Resources {
				constraint := constraintByID[resource.ConstraintID]
				age := checkedAt.Sub(resource.ObservedAt)
				if resource.Evidence != agent.EvidenceMeasured ||
					age > time.Duration(constraint.MaxObservationAgeSeconds)*time.Second {
					t.Fatalf("reusable advice contains weak/stale resource: %#v", resource)
				}
			}
		}
	}
}
