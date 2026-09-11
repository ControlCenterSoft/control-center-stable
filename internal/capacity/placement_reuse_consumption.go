package capacity

import (
	"encoding/json"
	"fmt"
)

const PlacementAdviceReuseConsumptionSchemaV1 = "capacity.placement-advice-reuse-consumption/v1"

// PlacementAdviceReuseConsumption is the last advisory-only gate before an
// already-approved placement recommendation is exposed to a consumer. It binds
// the saved reuse decision to a fresh revalidation of the exact evidence
// lineage. It never authorizes placement or any production mutation.
type PlacementAdviceReuseConsumption struct {
	SchemaVersion                 string                            `json:"schema_version"`
	ConsumptionID                 string                            `json:"consumption_id"`
	DecisionID                    string                            `json:"decision_id"`
	DecisionRevalidationID        string                            `json:"decision_revalidation_id"`
	CurrentRevalidationID         string                            `json:"current_revalidation_id"`
	SnapshotID                    string                            `json:"snapshot_id"`
	CurrentSnapshotID             string                            `json:"current_snapshot_id,omitempty"`
	PlacementSnapshotID           string                            `json:"placement_snapshot_id"`
	CurrentPlacementSnapshotID    string                            `json:"current_placement_snapshot_id"`
	ProvenanceSnapshotID          string                            `json:"provenance_snapshot_id"`
	CurrentProvenanceSnapshotID   string                            `json:"current_provenance_snapshot_id"`
	DerivationFingerprint         string                            `json:"derivation_fingerprint"`
	CurrentDerivationFingerprint  string                            `json:"current_derivation_fingerprint"`
	CurrentEvidenceStatus         PlacementAdviceRevalidationStatus `json:"current_evidence_status"`
	CurrentRecommendationReusable bool                              `json:"current_recommendation_reusable"`
	Status                        PlacementAdviceReuseStatus        `json:"status"`
	Reason                        string                            `json:"reason"`
	RecommendedAction             string                            `json:"recommended_action"`
	ReuseAllowed                  bool                              `json:"reuse_allowed"`
	AdvisoryOnly                  bool                              `json:"advisory_only"`
	PlacementAuthorized           bool                              `json:"placement_authorized"`
	ProductionMutation            bool                              `json:"production_mutation"`
}

// EvaluatePlacementAdviceReuseConsumption revalidates the boundary between a
// saved reuse decision and the latest strong evidence assessment. Ordinary
// evidence drift is represented as an explainable blocked result, while
// malformed or unsafe evidence is rejected fail-closed.
func EvaluatePlacementAdviceReuseConsumption(
	decision PlacementAdviceReuseDecision,
	current PlacementAdviceDerivedEvidenceRevalidation,
) (PlacementAdviceReuseConsumption, error) {
	if err := ValidatePlacementAdviceReuseDecision(decision); err != nil {
		return PlacementAdviceReuseConsumption{}, err
	}
	if err := ValidatePlacementAdviceDerivedEvidenceRevalidation(current); err != nil {
		return PlacementAdviceReuseConsumption{}, err
	}

	result := PlacementAdviceReuseConsumption{
		SchemaVersion:                 PlacementAdviceReuseConsumptionSchemaV1,
		DecisionID:                    decision.DecisionID,
		DecisionRevalidationID:        decision.RevalidationID,
		CurrentRevalidationID:         current.RevalidationID,
		SnapshotID:                    decision.SnapshotID,
		CurrentSnapshotID:             current.CurrentSnapshotID,
		PlacementSnapshotID:           decision.PlacementSnapshotID,
		CurrentPlacementSnapshotID:    current.CurrentPlacementSnapshotID,
		ProvenanceSnapshotID:          decision.ProvenanceSnapshotID,
		CurrentProvenanceSnapshotID:   current.CurrentProvenanceSnapshotID,
		DerivationFingerprint:         decision.DerivationFingerprint,
		CurrentDerivationFingerprint:  current.CurrentDerivationFingerprint,
		CurrentEvidenceStatus:         current.Status,
		CurrentRecommendationReusable: current.RecommendationReusable,
		Status:                        PlacementAdviceReuseBlocked,
		Reason:                        current.Reason,
		RecommendedAction:             current.RecommendedAction,
		ReuseAllowed:                  false,
		AdvisoryOnly:                  true,
		PlacementAuthorized:           false,
		ProductionMutation:            false,
	}

	switch {
	case decision.Status != PlacementAdviceReuseAllowed || !decision.ReuseAllowed:
		result.Reason = "reuse-decision-not-allowed"
		result.RecommendedAction = decision.RecommendedAction
	case current.Status != PlacementAdviceEvidenceCurrent || !current.RecommendationReusable:
		// Preserve the latest revalidation reason/action so callers know whether
		// telemetry, profile evidence, constraints, or derived evidence changed.
	case placementAdviceReuseLineageMatches(decision, current):
		result.Status = PlacementAdviceReuseAllowed
		result.Reason = "exact-current-reuse-decision-consumable"
		result.RecommendedAction = "none"
		result.ReuseAllowed = true
	default:
		result.Reason = "placement-reuse-lineage-drift"
		result.RecommendedAction = "rebuild-placement-advice-from-current-evidence"
	}

	result.ConsumptionID = placementAdviceReuseConsumptionID(result)
	if err := validatePlacementAdviceReuseConsumption(result); err != nil {
		return PlacementAdviceReuseConsumption{}, err
	}
	return result, nil
}

// ValidatePlacementAdviceReuseConsumption checks the immutable contract and
// its non-mutation boundary. Freshness at use time still requires evaluating
// the consumption against a newly produced strong-evidence revalidation.
func ValidatePlacementAdviceReuseConsumption(result PlacementAdviceReuseConsumption) error {
	return validatePlacementAdviceReuseConsumption(result)
}

func validatePlacementAdviceReuseConsumption(result PlacementAdviceReuseConsumption) error {
	if result.SchemaVersion != PlacementAdviceReuseConsumptionSchemaV1 ||
		!result.AdvisoryOnly || result.PlacementAuthorized || result.ProductionMutation {
		return fmt.Errorf("%w: unsafe placement reuse consumption", ErrInvalidRecommendation)
	}
	if result.Status != PlacementAdviceReuseAllowed && result.Status != PlacementAdviceReuseBlocked {
		return fmt.Errorf("%w: invalid placement reuse consumption status", ErrInvalidRecommendation)
	}
	if result.ConsumptionID == "" || result.DecisionID == "" ||
		result.DecisionRevalidationID == "" || result.CurrentRevalidationID == "" ||
		result.SnapshotID == "" || result.PlacementSnapshotID == "" ||
		result.CurrentPlacementSnapshotID == "" || result.ProvenanceSnapshotID == "" ||
		result.CurrentProvenanceSnapshotID == "" || result.DerivationFingerprint == "" ||
		result.CurrentDerivationFingerprint == "" || result.Reason == "" ||
		result.RecommendedAction == "" {
		return fmt.Errorf("%w: incomplete placement reuse consumption", ErrInvalidRecommendation)
	}
	if result.CurrentEvidenceStatus != PlacementAdviceEvidenceCurrent &&
		result.CurrentEvidenceStatus != PlacementAdviceEvidenceStale {
		return fmt.Errorf("%w: invalid current evidence status", ErrInvalidRecommendation)
	}
	if result.CurrentEvidenceStatus == PlacementAdviceEvidenceStale &&
		result.CurrentRecommendationReusable {
		return fmt.Errorf("%w: stale current evidence marked reusable", ErrInvalidRecommendation)
	}
	if result.Status == PlacementAdviceReuseAllowed {
		if !result.ReuseAllowed || !result.CurrentRecommendationReusable ||
			result.CurrentEvidenceStatus != PlacementAdviceEvidenceCurrent ||
			result.DecisionRevalidationID != result.CurrentRevalidationID ||
			result.CurrentSnapshotID == "" || result.CurrentSnapshotID != result.SnapshotID ||
			result.CurrentPlacementSnapshotID != result.PlacementSnapshotID ||
			result.CurrentProvenanceSnapshotID != result.ProvenanceSnapshotID ||
			result.CurrentDerivationFingerprint != result.DerivationFingerprint ||
			result.Reason != "exact-current-reuse-decision-consumable" ||
			result.RecommendedAction != "none" {
			return fmt.Errorf("%w: inconsistent allowed placement reuse consumption", ErrInvalidRecommendation)
		}
	} else if result.ReuseAllowed || result.RecommendedAction == "none" {
		return fmt.Errorf("%w: inconsistent blocked placement reuse consumption", ErrInvalidRecommendation)
	}
	if placementAdviceReuseConsumptionID(result) != result.ConsumptionID {
		return fmt.Errorf("%w: placement reuse consumption integrity mismatch", ErrInvalidRecommendation)
	}
	return nil
}

func placementAdviceReuseLineageMatches(
	decision PlacementAdviceReuseDecision,
	current PlacementAdviceDerivedEvidenceRevalidation,
) bool {
	return decision.RevalidationID == current.RevalidationID &&
		decision.SnapshotID == current.SnapshotID &&
		decision.CurrentSnapshotID == current.CurrentSnapshotID &&
		decision.PlacementSnapshotID == current.PlacementSnapshotID &&
		decision.CurrentPlacementSnapshotID == current.CurrentPlacementSnapshotID &&
		decision.ProvenanceSnapshotID == current.ProvenanceSnapshotID &&
		decision.CurrentProvenanceSnapshotID == current.CurrentProvenanceSnapshotID &&
		decision.DerivationFingerprint == current.DerivationFingerprint &&
		decision.CurrentDerivationFingerprint == current.CurrentDerivationFingerprint
}

func placementAdviceReuseConsumptionID(result PlacementAdviceReuseConsumption) string {
	canonical := struct {
		DecisionID                    string                            `json:"decision_id"`
		DecisionRevalidationID        string                            `json:"decision_revalidation_id"`
		CurrentRevalidationID         string                            `json:"current_revalidation_id"`
		SnapshotID                    string                            `json:"snapshot_id"`
		CurrentSnapshotID             string                            `json:"current_snapshot_id,omitempty"`
		PlacementSnapshotID           string                            `json:"placement_snapshot_id"`
		CurrentPlacementSnapshotID    string                            `json:"current_placement_snapshot_id"`
		ProvenanceSnapshotID          string                            `json:"provenance_snapshot_id"`
		CurrentProvenanceSnapshotID   string                            `json:"current_provenance_snapshot_id"`
		DerivationFingerprint         string                            `json:"derivation_fingerprint"`
		CurrentDerivationFingerprint  string                            `json:"current_derivation_fingerprint"`
		CurrentEvidenceStatus         PlacementAdviceRevalidationStatus `json:"current_evidence_status"`
		CurrentRecommendationReusable bool                              `json:"current_recommendation_reusable"`
		Status                        PlacementAdviceReuseStatus        `json:"status"`
		Reason                        string                            `json:"reason"`
		RecommendedAction             string                            `json:"recommended_action"`
		ReuseAllowed                  bool                              `json:"reuse_allowed"`
	}{
		result.DecisionID,
		result.DecisionRevalidationID,
		result.CurrentRevalidationID,
		result.SnapshotID,
		result.CurrentSnapshotID,
		result.PlacementSnapshotID,
		result.CurrentPlacementSnapshotID,
		result.ProvenanceSnapshotID,
		result.CurrentProvenanceSnapshotID,
		result.DerivationFingerprint,
		result.CurrentDerivationFingerprint,
		result.CurrentEvidenceStatus,
		result.CurrentRecommendationReusable,
		result.Status,
		result.Reason,
		result.RecommendedAction,
		result.ReuseAllowed,
	}
	encoded, _ := json.Marshal(canonical)
	return "parc-" + placementEvidenceSHA256Hex(encoded)[:24]
}
