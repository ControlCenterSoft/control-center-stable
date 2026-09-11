package capacity

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func placementResourceReuseGateFixture(t *testing.T) (
	PlacementRequest,
	PlacementAdviceDerivedEvidenceSnapshot,
	PlacementNodeDerivationInput,
	PlacementResourceHeadroomEnvelope,
	PlacementResourceHeadroomFreshnessGate,
	PlacementAdviceReuseDecision,
	time.Time,
) {
	t.Helper()
	request, derived, input, envelope, evaluatedAt := placementResourceHeadroomFreshnessFixture(t)
	source, err := RevalidatePlacementAdviceDerivedEvidence(
		derived,
		request,
		[]PlacementNodeDerivationInput{input},
		evaluatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := EvaluatePlacementAdviceReuse(source)
	if err != nil {
		t.Fatal(err)
	}
	checkedAt := evaluatedAt.Add(10 * time.Minute)
	freshness, err := BuildPlacementResourceHeadroomFreshnessGate(
		envelope,
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		checkedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	return request, derived, input, envelope, freshness, decision, checkedAt
}

func TestPlacementResourceReuseGateAllowsOnlyExactCurrentResourceEvidence(t *testing.T) {
	request, derived, input, envelope, freshness, decision, checkedAt :=
		placementResourceReuseGateFixture(t)

	got, err := EvaluatePlacementResourceReuseGate(
		decision,
		freshness,
		envelope,
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		checkedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != PlacementResourceReuseGateSchemaV1 || got.GateID == "" ||
		got.Status != PlacementAdviceReuseAllowed || !got.ReuseAllowed || got.RebuildRequired ||
		got.BlockedBy != PlacementResourceReuseBlockNone ||
		got.Reason != "exact-current-resource-headroom-reusable" ||
		got.RecommendedAction != "none" || !got.AdvisoryOnly ||
		got.PlacementAuthorized || got.ProductionMutation {
		t.Fatalf("exact current resource evidence was not safely reusable: %#v", got)
	}
	if got.DecisionID != decision.DecisionID ||
		got.ResourceFreshnessGateID != freshness.GateID ||
		got.HeadroomEnvelopeID != envelope.EnvelopeID ||
		got.DerivedSnapshotID != derived.SnapshotID ||
		got.PlacementSnapshotID != derived.Placement.SnapshotID ||
		got.AdviceID != derived.Placement.Advice.AdviceID ||
		got.ScopeID != derived.Placement.Advice.ScopeID {
		t.Fatalf("resource reuse lineage mismatch: %#v", got)
	}
	if err := ValidatePlacementResourceReuseGate(
		got,
		decision,
		freshness,
		envelope,
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		checkedAt,
	); err != nil {
		t.Fatal(err)
	}
}

func TestPlacementResourceReuseGateBlocksExpiredResourceHeadroom(t *testing.T) {
	request, derived, input, envelope, _, decision, _ := placementResourceReuseGateFixture(t)
	checkedAt := contractNow.Add(59*time.Minute + time.Second)
	freshness, err := BuildPlacementResourceHeadroomFreshnessGate(
		envelope,
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		checkedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if freshness.AdviceReusePermitted {
		t.Fatalf("fixture did not expire resource evidence: %#v", freshness)
	}

	got, err := EvaluatePlacementResourceReuseGate(
		decision,
		freshness,
		envelope,
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		checkedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != PlacementAdviceReuseBlocked || got.ReuseAllowed || !got.RebuildRequired ||
		got.BlockedBy != PlacementResourceReuseBlockResource ||
		got.Reason != "resource-headroom-evidence-not-current" ||
		got.RecommendedAction != "collect-current-placement-evidence" {
		t.Fatalf("expired resource evidence remained reusable: %#v", got)
	}
}

func TestPlacementResourceReuseGateKeepsBlockedStrongDecisionBlocked(t *testing.T) {
	request, derived, input, envelope, freshness, _, checkedAt :=
		placementResourceReuseGateFixture(t)
	source, err := RevalidatePlacementAdviceDerivedEvidence(
		derived,
		request,
		[]PlacementNodeDerivationInput{input},
		derived.DerivationProvenance.Provenance.EvaluatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	source.RecommendationReusable = false
	source.RevalidationID = placementAdviceDerivedEvidenceRevalidationID(source)
	decision, err := EvaluatePlacementAdviceReuse(source)
	if err != nil {
		t.Fatal(err)
	}
	if decision.ReuseAllowed {
		t.Fatalf("fixture decision unexpectedly reusable: %#v", decision)
	}

	got, err := EvaluatePlacementResourceReuseGate(
		decision,
		freshness,
		envelope,
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		checkedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != PlacementAdviceReuseBlocked || got.ReuseAllowed || !got.RebuildRequired ||
		got.BlockedBy != PlacementResourceReuseBlockStrong ||
		got.Reason != "strong-evidence-reuse-blocked" ||
		got.RecommendedAction != "rebuild-placement-advice-from-current-evidence" {
		t.Fatalf("blocked strong evidence escaped resource gate: %#v", got)
	}
}

func TestPlacementResourceReuseGateBlocksCrossLineageDecision(t *testing.T) {
	request, derived, input, envelope, freshness, decision, checkedAt :=
		placementResourceReuseGateFixture(t)

	decision.SnapshotID = "pade-" + strings.Repeat("f", 24)
	decision.CurrentSnapshotID = decision.SnapshotID
	decision.DecisionID = placementAdviceReuseDecisionID(decision)
	if err := ValidatePlacementAdviceReuseDecision(decision); err != nil {
		t.Fatal(err)
	}

	got, err := EvaluatePlacementResourceReuseGate(
		decision,
		freshness,
		envelope,
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		checkedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != PlacementAdviceReuseBlocked || got.ReuseAllowed || !got.RebuildRequired ||
		got.BlockedBy != PlacementResourceReuseBlockLineage ||
		got.Reason != "placement-resource-lineage-drift" ||
		got.RecommendedAction != "rebuild-placement-advice-from-current-evidence" {
		t.Fatalf("cross-lineage decision remained reusable: %#v", got)
	}
}

func TestPlacementResourceReuseGateIsDeterministicAndTamperEvident(t *testing.T) {
	request, derived, input, envelope, freshness, decision, checkedAt :=
		placementResourceReuseGateFixture(t)
	first, err := EvaluatePlacementResourceReuseGate(
		decision,
		freshness,
		envelope,
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		checkedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		next, err := EvaluatePlacementResourceReuseGate(
			decision,
			freshness,
			envelope,
			request,
			derived,
			[]PlacementNodeDerivationInput{input},
			checkedAt,
		)
		if err != nil {
			t.Fatal(err)
		}
		if next != first {
			t.Fatalf("same inputs changed gate at iteration %d: %#v %#v", i, first, next)
		}
	}

	tampered := first
	tampered.PlacementAuthorized = true
	if err := ValidatePlacementResourceReuseGate(
		tampered,
		decision,
		freshness,
		envelope,
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		checkedAt,
	); !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("placement authority tamper error = %v", err)
	}

	tamperedFreshness := freshness
	tamperedFreshness.AdviceReusePermitted = false
	if _, err := EvaluatePlacementResourceReuseGate(
		decision,
		tamperedFreshness,
		envelope,
		request,
		derived,
		[]PlacementNodeDerivationInput{input},
		checkedAt,
	); !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("freshness tamper error = %v", err)
	}
}

func TestPlacementResourceReuseGateReuseImpliesBothUpstreamGatesAllow(t *testing.T) {
	request, derived, input, envelope, freshness, decision, checkedAt :=
		placementResourceReuseGateFixture(t)

	for _, strongAllowed := range []bool{false, true} {
		for _, resourceAllowed := range []bool{false, true} {
			candidateDecision := decision
			if !strongAllowed {
				source, err := RevalidatePlacementAdviceDerivedEvidence(
					derived,
					request,
					[]PlacementNodeDerivationInput{input},
					derived.DerivationProvenance.Provenance.EvaluatedAt,
				)
				if err != nil {
					t.Fatal(err)
				}
				source.RecommendationReusable = false
				source.RevalidationID = placementAdviceDerivedEvidenceRevalidationID(source)
				candidateDecision, err = EvaluatePlacementAdviceReuse(source)
				if err != nil {
					t.Fatal(err)
				}
			}

			candidateFreshness := freshness
			candidateCheckedAt := checkedAt
			if !resourceAllowed {
				candidateCheckedAt = contractNow.Add(59*time.Minute + time.Second)
				var err error
				candidateFreshness, err = BuildPlacementResourceHeadroomFreshnessGate(
					envelope,
					request,
					derived,
					[]PlacementNodeDerivationInput{input},
					candidateCheckedAt,
				)
				if err != nil {
					t.Fatal(err)
				}
			}

			got, err := EvaluatePlacementResourceReuseGate(
				candidateDecision,
				candidateFreshness,
				envelope,
				request,
				derived,
				[]PlacementNodeDerivationInput{input},
				candidateCheckedAt,
			)
			if err != nil {
				t.Fatal(err)
			}
			if got.ReuseAllowed != (strongAllowed && resourceAllowed) {
				t.Fatalf(
					"reuse invariant failed strong=%v resource=%v: %#v",
					strongAllowed,
					resourceAllowed,
					got,
				)
			}
			if got.ReuseAllowed && (got.PlacementAuthorized || got.ProductionMutation) {
				t.Fatalf("reuse granted mutation authority: %#v", got)
			}
		}
	}
}
