package capacity

import (
	"encoding/json"
	"fmt"
)

const PlacementAdviceReuseDecisionSchemaV1 = "capacity.placement-advice-reuse-decision/v1"

type PlacementAdviceReuseStatus string

const (
	PlacementAdviceReuseAllowed PlacementAdviceReuseStatus = "allowed"
	PlacementAdviceReuseBlocked PlacementAdviceReuseStatus = "blocked"
)

// PlacementAdviceReuseDecision is the bounded consumer gate for a previously
// revalidated strong placement recommendation. It can permit reuse of advisory
// evidence, but never authorizes placement or any production mutation.
type PlacementAdviceReuseDecision struct {
	SchemaVersion                string                     `json:"schema_version"`
	DecisionID                   string                     `json:"decision_id"`
	RevalidationID               string                     `json:"revalidation_id"`
	SnapshotID                   string                     `json:"snapshot_id"`
	CurrentSnapshotID            string                     `json:"current_snapshot_id,omitempty"`
	PlacementSnapshotID          string                     `json:"placement_snapshot_id"`
	CurrentPlacementSnapshotID   string                     `json:"current_placement_snapshot_id"`
	ProvenanceSnapshotID         string                     `json:"provenance_snapshot_id"`
	CurrentProvenanceSnapshotID  string                     `json:"current_provenance_snapshot_id"`
	DerivationFingerprint        string                     `json:"derivation_fingerprint"`
	CurrentDerivationFingerprint string                     `json:"current_derivation_fingerprint"`
	Status                       PlacementAdviceReuseStatus `json:"status"`
	Reason                       string                     `json:"reason"`
	RecommendedAction            string                     `json:"recommended_action"`
	ReuseAllowed                 bool                       `json:"reuse_allowed"`
	AdvisoryOnly                 bool                       `json:"advisory_only"`
	PlacementAuthorized          bool                       `json:"placement_authorized"`
	ProductionMutation           bool                       `json:"production_mutation"`
}

// EvaluatePlacementAdviceReuse converts exact revalidation evidence into a
// consumer decision. Stale evidence remains explainably blocked; current
// evidence is reusable only when the upstream revalidation explicitly marks
// the recommendation reusable.
func EvaluatePlacementAdviceReuse(
	revalidation PlacementAdviceDerivedEvidenceRevalidation,
) (PlacementAdviceReuseDecision, error) {
	if err := ValidatePlacementAdviceDerivedEvidenceRevalidation(revalidation); err != nil {
		return PlacementAdviceReuseDecision{}, err
	}

	result := PlacementAdviceReuseDecision{
		SchemaVersion:                PlacementAdviceReuseDecisionSchemaV1,
		RevalidationID:               revalidation.RevalidationID,
		SnapshotID:                   revalidation.SnapshotID,
		CurrentSnapshotID:            revalidation.CurrentSnapshotID,
		PlacementSnapshotID:          revalidation.PlacementSnapshotID,
		CurrentPlacementSnapshotID:   revalidation.CurrentPlacementSnapshotID,
		ProvenanceSnapshotID:         revalidation.ProvenanceSnapshotID,
		CurrentProvenanceSnapshotID:  revalidation.CurrentProvenanceSnapshotID,
		DerivationFingerprint:        revalidation.DerivationFingerprint,
		CurrentDerivationFingerprint: revalidation.CurrentDerivationFingerprint,
		Status:                       PlacementAdviceReuseBlocked,
		Reason:                       revalidation.Reason,
		RecommendedAction:            revalidation.RecommendedAction,
		ReuseAllowed:                 false,
		AdvisoryOnly:                 true,
		PlacementAuthorized:          false,
		ProductionMutation:           false,
	}

	if revalidation.Status == PlacementAdviceEvidenceCurrent {
		if revalidation.RecommendationReusable {
			result.Status = PlacementAdviceReuseAllowed
			result.Reason = "exact-current-derived-evidence-reusable"
			result.RecommendedAction = "none"
			result.ReuseAllowed = true
		} else {
			result.Reason = "current-derived-evidence-not-reusable"
			result.RecommendedAction = "rebuild-placement-advice-from-current-evidence"
		}
	}

	result.DecisionID = placementAdviceReuseDecisionID(result)
	if err := validatePlacementAdviceReuseDecision(result); err != nil {
		return PlacementAdviceReuseDecision{}, err
	}
	return result, nil
}

// ValidatePlacementAdviceReuseDecision verifies decision integrity and the
// advisory-only safety boundary without granting any execution authority.
func ValidatePlacementAdviceReuseDecision(result PlacementAdviceReuseDecision) error {
	return validatePlacementAdviceReuseDecision(result)
}

func validatePlacementAdviceReuseDecision(result PlacementAdviceReuseDecision) error {
	if result.SchemaVersion != PlacementAdviceReuseDecisionSchemaV1 ||
		!result.AdvisoryOnly || result.PlacementAuthorized || result.ProductionMutation {
		return fmt.Errorf("%w: unsafe placement reuse decision", ErrInvalidRecommendation)
	}
	if result.Status != PlacementAdviceReuseAllowed && result.Status != PlacementAdviceReuseBlocked {
		return fmt.Errorf("%w: invalid placement reuse status", ErrInvalidRecommendation)
	}
	if result.DecisionID == "" || result.RevalidationID == "" || result.SnapshotID == "" ||
		result.PlacementSnapshotID == "" || result.CurrentPlacementSnapshotID == "" ||
		result.ProvenanceSnapshotID == "" || result.CurrentProvenanceSnapshotID == "" ||
		result.DerivationFingerprint == "" || result.CurrentDerivationFingerprint == "" ||
		result.Reason == "" || result.RecommendedAction == "" {
		return fmt.Errorf("%w: incomplete placement reuse decision", ErrInvalidRecommendation)
	}
	if result.Status == PlacementAdviceReuseAllowed {
		if !result.ReuseAllowed || result.CurrentSnapshotID == "" ||
			result.CurrentSnapshotID != result.SnapshotID ||
			result.CurrentPlacementSnapshotID != result.PlacementSnapshotID ||
			result.CurrentProvenanceSnapshotID != result.ProvenanceSnapshotID ||
			result.CurrentDerivationFingerprint != result.DerivationFingerprint ||
			result.Reason != "exact-current-derived-evidence-reusable" ||
			result.RecommendedAction != "none" {
			return fmt.Errorf("%w: inconsistent allowed placement reuse decision", ErrInvalidRecommendation)
		}
	} else {
		if result.ReuseAllowed || result.RecommendedAction == "none" {
			return fmt.Errorf("%w: inconsistent blocked placement reuse decision", ErrInvalidRecommendation)
		}
	}
	if placementAdviceReuseDecisionID(result) != result.DecisionID {
		return fmt.Errorf("%w: placement reuse decision integrity mismatch", ErrInvalidRecommendation)
	}
	return nil
}

func placementAdviceReuseDecisionID(result PlacementAdviceReuseDecision) string {
	canonical := struct {
		RevalidationID               string                     `json:"revalidation_id"`
		SnapshotID                   string                     `json:"snapshot_id"`
		CurrentSnapshotID            string                     `json:"current_snapshot_id,omitempty"`
		PlacementSnapshotID          string                     `json:"placement_snapshot_id"`
		CurrentPlacementSnapshotID   string                     `json:"current_placement_snapshot_id"`
		ProvenanceSnapshotID         string                     `json:"provenance_snapshot_id"`
		CurrentProvenanceSnapshotID  string                     `json:"current_provenance_snapshot_id"`
		DerivationFingerprint        string                     `json:"derivation_fingerprint"`
		CurrentDerivationFingerprint string                     `json:"current_derivation_fingerprint"`
		Status                       PlacementAdviceReuseStatus `json:"status"`
		Reason                       string                     `json:"reason"`
		RecommendedAction            string                     `json:"recommended_action"`
		ReuseAllowed                 bool                       `json:"reuse_allowed"`
	}{
		result.RevalidationID,
		result.SnapshotID,
		result.CurrentSnapshotID,
		result.PlacementSnapshotID,
		result.CurrentPlacementSnapshotID,
		result.ProvenanceSnapshotID,
		result.CurrentProvenanceSnapshotID,
		result.DerivationFingerprint,
		result.CurrentDerivationFingerprint,
		result.Status,
		result.Reason,
		result.RecommendedAction,
		result.ReuseAllowed,
	}
	encoded, _ := json.Marshal(canonical)
	return "pard-" + placementEvidenceSHA256Hex(encoded)[:24]
}
