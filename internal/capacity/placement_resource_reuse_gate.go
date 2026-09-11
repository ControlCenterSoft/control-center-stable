package capacity

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

const PlacementResourceReuseGateSchemaV1 = "capacity.placement-resource-reuse-gate/v1"

const (
	PlacementResourceReuseBlockNone     = "none"
	PlacementResourceReuseBlockStrong   = "strong-evidence"
	PlacementResourceReuseBlockResource = "resource-evidence"
	PlacementResourceReuseBlockLineage  = "lineage"
)

// PlacementResourceReuseGate combines the existing strong-evidence reuse
// boundary with the per-resource freshness gate. It only permits reuse of an
// already-advisory recommendation; it never authorizes placement or mutation.
type PlacementResourceReuseGate struct {
	SchemaVersion           string                     `json:"schema_version"`
	GateID                  string                     `json:"gate_id"`
	DecisionID              string                     `json:"decision_id"`
	ReuseConsumptionID      string                     `json:"reuse_consumption_id"`
	StrongRevalidationID    string                     `json:"strong_revalidation_id"`
	ResourceFreshnessGateID string                     `json:"resource_freshness_gate_id"`
	HeadroomEnvelopeID      string                     `json:"headroom_envelope_id"`
	DerivedSnapshotID       string                     `json:"derived_snapshot_id"`
	PlacementSnapshotID     string                     `json:"placement_snapshot_id"`
	AdviceID                string                     `json:"advice_id"`
	ScopeID                 string                     `json:"scope_id"`
	CheckedAt               time.Time                  `json:"checked_at"`
	Status                  PlacementAdviceReuseStatus `json:"status"`
	BlockedBy               string                     `json:"blocked_by"`
	Reason                  string                     `json:"reason"`
	RecommendedAction       string                     `json:"recommended_action"`
	ReuseAllowed            bool                       `json:"reuse_allowed"`
	RebuildRequired         bool                       `json:"rebuild_required"`
	AdvisoryOnly            bool                       `json:"advisory_only"`
	PlacementAuthorized     bool                       `json:"placement_authorized"`
	ProductionMutation      bool                       `json:"production_mutation"`
}

// EvaluatePlacementResourceReuseGate consumes an exact reuse decision and an
// exact multi-resource freshness gate. The strong-evidence consumption is
// reconstructed from the sealed derived snapshot at its original evaluation
// boundary; the resource gate is independently rebuilt at checkedAt. Reuse is
// allowed only when both boundaries remain exact and current.
func EvaluatePlacementResourceReuseGate(
	decision PlacementAdviceReuseDecision,
	freshness PlacementResourceHeadroomFreshnessGate,
	envelope PlacementResourceHeadroomEnvelope,
	request PlacementRequest,
	derived PlacementAdviceDerivedEvidenceSnapshot,
	inputs []PlacementNodeDerivationInput,
	checkedAt time.Time,
) (PlacementResourceReuseGate, error) {
	if checkedAt.IsZero() {
		return PlacementResourceReuseGate{}, fmt.Errorf(
			"%w: checked_at is required",
			ErrInvalidRecommendation,
		)
	}
	checkedAt = checkedAt.UTC()

	sourceEvaluatedAt := derived.DerivationProvenance.Provenance.EvaluatedAt
	current, err := RevalidatePlacementAdviceDerivedEvidence(
		derived,
		request,
		inputs,
		sourceEvaluatedAt,
	)
	if err != nil {
		return PlacementResourceReuseGate{}, err
	}
	consumption, err := EvaluatePlacementAdviceReuseConsumption(decision, current)
	if err != nil {
		return PlacementResourceReuseGate{}, err
	}
	if err := ValidatePlacementResourceHeadroomFreshnessGate(
		freshness,
		envelope,
		request,
		derived,
		inputs,
		checkedAt,
	); err != nil {
		return PlacementResourceReuseGate{}, err
	}

	result := PlacementResourceReuseGate{
		SchemaVersion:           PlacementResourceReuseGateSchemaV1,
		DecisionID:              decision.DecisionID,
		ReuseConsumptionID:      consumption.ConsumptionID,
		StrongRevalidationID:    current.RevalidationID,
		ResourceFreshnessGateID: freshness.GateID,
		HeadroomEnvelopeID:      envelope.EnvelopeID,
		DerivedSnapshotID:       derived.SnapshotID,
		PlacementSnapshotID:     derived.Placement.SnapshotID,
		AdviceID:                derived.Placement.Advice.AdviceID,
		ScopeID:                 derived.Placement.Advice.ScopeID,
		CheckedAt:               checkedAt,
		Status:                  PlacementAdviceReuseBlocked,
		BlockedBy:               PlacementResourceReuseBlockStrong,
		Reason:                  "strong-evidence-reuse-blocked",
		RecommendedAction:       "rebuild-placement-advice-from-current-evidence",
		ReuseAllowed:            false,
		RebuildRequired:         true,
		AdvisoryOnly:            true,
		PlacementAuthorized:     false,
		ProductionMutation:      false,
	}

	switch {
	case !placementResourceReuseLineageMatches(decision, consumption, freshness, envelope, derived):
		result.BlockedBy = PlacementResourceReuseBlockLineage
		result.Reason = "placement-resource-lineage-drift"
	case !freshness.AllCurrent || !freshness.AdviceReusePermitted:
		result.BlockedBy = PlacementResourceReuseBlockResource
		result.Reason = "resource-headroom-evidence-not-current"
		result.RecommendedAction = "collect-current-placement-evidence"
	case consumption.Status != PlacementAdviceReuseAllowed || !consumption.ReuseAllowed:
		// Keep the result normalized to a rebuild requirement. The exact
		// upstream consumption remains addressable by ReuseConsumptionID.
	default:
		result.Status = PlacementAdviceReuseAllowed
		result.BlockedBy = PlacementResourceReuseBlockNone
		result.Reason = "exact-current-resource-headroom-reusable"
		result.RecommendedAction = "none"
		result.ReuseAllowed = true
		result.RebuildRequired = false
	}

	result.GateID = placementResourceReuseGateID(result)
	if err := validatePlacementResourceReuseGate(result); err != nil {
		return PlacementResourceReuseGate{}, err
	}
	return result, nil
}

// ValidatePlacementResourceReuseGate rebuilds the exact gate from source
// evidence and rejects any change to lineage, verdict, or safety authority.
func ValidatePlacementResourceReuseGate(
	gate PlacementResourceReuseGate,
	decision PlacementAdviceReuseDecision,
	freshness PlacementResourceHeadroomFreshnessGate,
	envelope PlacementResourceHeadroomEnvelope,
	request PlacementRequest,
	derived PlacementAdviceDerivedEvidenceSnapshot,
	inputs []PlacementNodeDerivationInput,
	checkedAt time.Time,
) error {
	expected, err := EvaluatePlacementResourceReuseGate(
		decision,
		freshness,
		envelope,
		request,
		derived,
		inputs,
		checkedAt,
	)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, gate) {
		return fmt.Errorf(
			"%w: placement resource reuse gate was modified",
			ErrInvalidRecommendation,
		)
	}
	return nil
}

func placementResourceReuseLineageMatches(
	decision PlacementAdviceReuseDecision,
	consumption PlacementAdviceReuseConsumption,
	freshness PlacementResourceHeadroomFreshnessGate,
	envelope PlacementResourceHeadroomEnvelope,
	derived PlacementAdviceDerivedEvidenceSnapshot,
) bool {
	return consumption.DecisionID == decision.DecisionID &&
		consumption.DecisionRevalidationID == decision.RevalidationID &&
		decision.SnapshotID == derived.SnapshotID &&
		consumption.SnapshotID == derived.SnapshotID &&
		decision.PlacementSnapshotID == derived.Placement.SnapshotID &&
		consumption.PlacementSnapshotID == derived.Placement.SnapshotID &&
		freshness.HeadroomEnvelopeID == envelope.EnvelopeID &&
		freshness.DerivedSnapshotID == derived.SnapshotID &&
		freshness.PlacementSnapshotID == derived.Placement.SnapshotID &&
		freshness.AdviceID == derived.Placement.Advice.AdviceID &&
		freshness.ScopeID == derived.Placement.Advice.ScopeID
}

func validatePlacementResourceReuseGate(result PlacementResourceReuseGate) error {
	if result.SchemaVersion != PlacementResourceReuseGateSchemaV1 ||
		!result.AdvisoryOnly || result.PlacementAuthorized || result.ProductionMutation {
		return fmt.Errorf("%w: unsafe placement resource reuse gate", ErrInvalidRecommendation)
	}
	if result.CheckedAt.IsZero() || result.CheckedAt.Location() != time.UTC {
		return fmt.Errorf("%w: checked_at must be canonical UTC", ErrInvalidRecommendation)
	}
	if result.GateID == "" || result.DecisionID == "" || result.ReuseConsumptionID == "" ||
		result.StrongRevalidationID == "" || result.ResourceFreshnessGateID == "" ||
		result.HeadroomEnvelopeID == "" || result.DerivedSnapshotID == "" ||
		result.PlacementSnapshotID == "" || result.AdviceID == "" || result.ScopeID == "" ||
		result.Reason == "" || result.RecommendedAction == "" {
		return fmt.Errorf("%w: incomplete placement resource reuse gate", ErrInvalidRecommendation)
	}
	if result.Status != PlacementAdviceReuseAllowed && result.Status != PlacementAdviceReuseBlocked {
		return fmt.Errorf("%w: invalid placement resource reuse status", ErrInvalidRecommendation)
	}

	if result.Status == PlacementAdviceReuseAllowed {
		if !result.ReuseAllowed || result.RebuildRequired ||
			result.BlockedBy != PlacementResourceReuseBlockNone ||
			result.Reason != "exact-current-resource-headroom-reusable" ||
			result.RecommendedAction != "none" {
			return fmt.Errorf("%w: inconsistent allowed placement resource reuse gate", ErrInvalidRecommendation)
		}
	} else {
		if result.ReuseAllowed || !result.RebuildRequired || result.RecommendedAction == "none" {
			return fmt.Errorf("%w: inconsistent blocked placement resource reuse gate", ErrInvalidRecommendation)
		}
		switch result.BlockedBy {
		case PlacementResourceReuseBlockStrong:
			if result.Reason != "strong-evidence-reuse-blocked" ||
				result.RecommendedAction != "rebuild-placement-advice-from-current-evidence" {
				return fmt.Errorf("%w: inconsistent strong-evidence block", ErrInvalidRecommendation)
			}
		case PlacementResourceReuseBlockResource:
			if result.Reason != "resource-headroom-evidence-not-current" ||
				result.RecommendedAction != "collect-current-placement-evidence" {
				return fmt.Errorf("%w: inconsistent resource-evidence block", ErrInvalidRecommendation)
			}
		case PlacementResourceReuseBlockLineage:
			if result.Reason != "placement-resource-lineage-drift" ||
				result.RecommendedAction != "rebuild-placement-advice-from-current-evidence" {
				return fmt.Errorf("%w: inconsistent lineage block", ErrInvalidRecommendation)
			}
		default:
			return fmt.Errorf("%w: invalid placement resource block reason", ErrInvalidRecommendation)
		}
	}
	if placementResourceReuseGateID(result) != result.GateID {
		return fmt.Errorf("%w: placement resource reuse gate integrity mismatch", ErrInvalidRecommendation)
	}
	return nil
}

func placementResourceReuseGateID(result PlacementResourceReuseGate) string {
	canonical := struct {
		DecisionID              string                     `json:"decision_id"`
		ReuseConsumptionID      string                     `json:"reuse_consumption_id"`
		StrongRevalidationID    string                     `json:"strong_revalidation_id"`
		ResourceFreshnessGateID string                     `json:"resource_freshness_gate_id"`
		HeadroomEnvelopeID      string                     `json:"headroom_envelope_id"`
		DerivedSnapshotID       string                     `json:"derived_snapshot_id"`
		PlacementSnapshotID     string                     `json:"placement_snapshot_id"`
		AdviceID                string                     `json:"advice_id"`
		ScopeID                 string                     `json:"scope_id"`
		CheckedAt               string                     `json:"checked_at"`
		Status                  PlacementAdviceReuseStatus `json:"status"`
		BlockedBy               string                     `json:"blocked_by"`
		Reason                  string                     `json:"reason"`
		RecommendedAction       string                     `json:"recommended_action"`
		ReuseAllowed            bool                       `json:"reuse_allowed"`
		RebuildRequired         bool                       `json:"rebuild_required"`
	}{
		result.DecisionID,
		result.ReuseConsumptionID,
		result.StrongRevalidationID,
		result.ResourceFreshnessGateID,
		result.HeadroomEnvelopeID,
		result.DerivedSnapshotID,
		result.PlacementSnapshotID,
		result.AdviceID,
		result.ScopeID,
		result.CheckedAt.Format(time.RFC3339Nano),
		result.Status,
		result.BlockedBy,
		result.Reason,
		result.RecommendedAction,
		result.ReuseAllowed,
		result.RebuildRequired,
	}
	encoded, _ := json.Marshal(canonical)
	return "prrg-" + placementEvidenceSHA256Hex(encoded)[:24]
}
