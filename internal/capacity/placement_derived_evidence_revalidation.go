package capacity

import (
	"encoding/json"
	"fmt"
	"time"
)

const PlacementAdviceDerivedEvidenceRevalidationSchemaV1 = "capacity.placement-advice-derived-evidence-revalidation/v1"

// PlacementAdviceDerivedEvidenceRevalidation is a fail-closed assessment of a
// previously sealed strong placement recommendation against current exact
// derivation inputs. It is advisory evidence only and never authorizes a
// placement or other infrastructure mutation.
type PlacementAdviceDerivedEvidenceRevalidation struct {
	SchemaVersion                string                            `json:"schema_version"`
	RevalidationID               string                            `json:"revalidation_id"`
	SnapshotID                   string                            `json:"snapshot_id"`
	CurrentSnapshotID            string                            `json:"current_snapshot_id,omitempty"`
	PlacementSnapshotID          string                            `json:"placement_snapshot_id"`
	CurrentPlacementSnapshotID   string                            `json:"current_placement_snapshot_id"`
	ProvenanceSnapshotID         string                            `json:"provenance_snapshot_id"`
	CurrentProvenanceSnapshotID  string                            `json:"current_provenance_snapshot_id"`
	DerivationFingerprint        string                            `json:"derivation_fingerprint"`
	CurrentDerivationFingerprint string                            `json:"current_derivation_fingerprint"`
	Status                       PlacementAdviceRevalidationStatus `json:"status"`
	Reason                       string                            `json:"reason"`
	StaleNodeID                  string                            `json:"stale_node_id,omitempty"`
	RecommendationReusable       bool                              `json:"recommendation_reusable"`
	RecommendedAction            string                            `json:"recommended_action"`
	AdvisoryOnly                 bool                              `json:"advisory_only"`
	ProductionMutation           bool                              `json:"production_mutation"`
}

// RevalidatePlacementAdviceDerivedEvidence accepts only the strong
// derivation-bound recommendation envelope and current exact derivation inputs.
// It replays placement and provenance validation at the explicit evaluation
// time. A current result is reusable only when every nested evidence layer is
// unchanged and the underlying recommendation is itself safely reusable.
func RevalidatePlacementAdviceDerivedEvidence(
	saved PlacementAdviceDerivedEvidenceSnapshot,
	request PlacementRequest,
	inputs []PlacementNodeDerivationInput,
	evaluatedAt time.Time,
) (PlacementAdviceDerivedEvidenceRevalidation, error) {
	if err := validatePlacementAdviceDerivedEvidenceSnapshot(saved); err != nil {
		return PlacementAdviceDerivedEvidenceRevalidation{}, err
	}

	nodes, _, bindings, err := validatePlacementNodeDerivations(inputs, evaluatedAt)
	if err != nil {
		return PlacementAdviceDerivedEvidenceRevalidation{}, err
	}
	currentDerivationFingerprint, err := placementNodeDerivationFingerprint(bindings)
	if err != nil {
		return PlacementAdviceDerivedEvidenceRevalidation{}, err
	}
	placementResult, err := RevalidatePlacementAdviceSnapshot(saved.Placement, request, nodes)
	if err != nil {
		return PlacementAdviceDerivedEvidenceRevalidation{}, err
	}
	provenanceResult, err := RevalidatePlacementAdviceDerivationProvenance(
		saved.DerivationProvenance,
		saved.Placement,
		request,
		inputs,
		evaluatedAt,
	)
	if err != nil {
		return PlacementAdviceDerivedEvidenceRevalidation{}, err
	}

	result := PlacementAdviceDerivedEvidenceRevalidation{
		SchemaVersion:                PlacementAdviceDerivedEvidenceRevalidationSchemaV1,
		SnapshotID:                   saved.SnapshotID,
		PlacementSnapshotID:          saved.Placement.SnapshotID,
		CurrentPlacementSnapshotID:   provenanceResult.CurrentPlacementSnapshotID,
		ProvenanceSnapshotID:         saved.DerivationProvenance.Provenance.ProvenanceSnapshotID,
		CurrentProvenanceSnapshotID:  provenanceResult.CurrentProvenanceSnapshotID,
		DerivationFingerprint:        saved.DerivationProvenance.DerivationFingerprint,
		CurrentDerivationFingerprint: currentDerivationFingerprint,
		Status:                       PlacementAdviceEvidenceStale,
		Reason:                       "placement-evidence-stale",
		RecommendationReusable:       false,
		RecommendedAction:            "rebuild-placement-advice-from-current-evidence",
		AdvisoryOnly:                 true,
		ProductionMutation:           false,
	}

	switch {
	case provenanceResult.Reason == "telemetry-freshness-expired":
		result.Reason = "telemetry-freshness-expired"
		result.StaleNodeID = provenanceResult.StaleNodeID
		result.RecommendedAction = "refresh-telemetry-evidence"
	case placementResult.Reason == "placement-request-or-constraint-drift":
		result.Reason = "placement-request-or-constraint-drift"
	case provenanceResult.Reason == "capacity-profile-revision-drift":
		result.Reason = "capacity-profile-revision-drift"
		result.StaleNodeID = provenanceResult.StaleNodeID
		result.RecommendedAction = "refresh-capacity-profile-evidence"
	case provenanceResult.Reason == "telemetry-revision-drift":
		result.Reason = "telemetry-revision-drift"
		result.StaleNodeID = provenanceResult.StaleNodeID
		result.RecommendedAction = "refresh-telemetry-evidence"
	case provenanceResult.Reason == "projection-derivation-drift":
		result.Reason = "projection-derivation-drift"
		result.StaleNodeID = provenanceResult.StaleNodeID
		result.RecommendedAction = "rebuild-node-projections-from-current-evidence"
	case placementResult.Reason == "node-capacity-or-telemetry-evidence-drift":
		result.Reason = "node-capacity-or-telemetry-evidence-drift"
	case placementResult.Reason == "placement-decision-drift":
		result.Reason = "placement-decision-drift"
	case placementResult.Reason == "snapshot-identity-drift":
		result.Reason = "snapshot-identity-drift"
	case placementResult.Status == PlacementAdviceEvidenceCurrent &&
		provenanceResult.Status == PlacementAdviceEvidenceCurrent:
		current, err := CapturePlacementAdviceFromDerivations(request, inputs, evaluatedAt)
		if err != nil {
			return PlacementAdviceDerivedEvidenceRevalidation{}, err
		}
		result.CurrentSnapshotID = current.SnapshotID
		if current.SnapshotID != saved.SnapshotID {
			result.Reason = "derived-evidence-identity-drift"
			break
		}
		result.Status = PlacementAdviceEvidenceCurrent
		result.Reason = "exact-derived-evidence-current"
		result.RecommendationReusable = placementResult.RecommendationReusable
		result.RecommendedAction = "none"
	default:
		result.Reason = provenanceResult.Reason
		result.StaleNodeID = provenanceResult.StaleNodeID
		result.RecommendedAction = provenanceResult.RecommendedAction
	}

	if result.Status == PlacementAdviceEvidenceStale &&
		result.Reason != "telemetry-freshness-expired" {
		current, captureErr := CapturePlacementAdviceFromDerivations(request, inputs, evaluatedAt)
		if captureErr != nil {
			return PlacementAdviceDerivedEvidenceRevalidation{}, captureErr
		}
		result.CurrentSnapshotID = current.SnapshotID
	}

	result.RevalidationID = placementAdviceDerivedEvidenceRevalidationID(result)
	if err := validatePlacementAdviceDerivedEvidenceRevalidation(result); err != nil {
		return PlacementAdviceDerivedEvidenceRevalidation{}, err
	}
	return result, nil
}

// ValidatePlacementAdviceDerivedEvidenceRevalidation verifies the immutable
// revalidation envelope without granting execution or placement authority.
func ValidatePlacementAdviceDerivedEvidenceRevalidation(
	result PlacementAdviceDerivedEvidenceRevalidation,
) error {
	return validatePlacementAdviceDerivedEvidenceRevalidation(result)
}

func validatePlacementAdviceDerivedEvidenceRevalidation(
	result PlacementAdviceDerivedEvidenceRevalidation,
) error {
	if result.SchemaVersion != PlacementAdviceDerivedEvidenceRevalidationSchemaV1 ||
		!result.AdvisoryOnly || result.ProductionMutation {
		return fmt.Errorf("%w: unsafe derived placement revalidation", ErrInvalidRecommendation)
	}
	if result.Status != PlacementAdviceEvidenceCurrent && result.Status != PlacementAdviceEvidenceStale {
		return fmt.Errorf("%w: invalid derived placement revalidation status", ErrInvalidRecommendation)
	}
	if result.RevalidationID == "" || result.SnapshotID == "" ||
		result.PlacementSnapshotID == "" || result.CurrentPlacementSnapshotID == "" ||
		result.ProvenanceSnapshotID == "" || result.CurrentProvenanceSnapshotID == "" ||
		result.DerivationFingerprint == "" || result.CurrentDerivationFingerprint == "" ||
		result.Reason == "" || result.RecommendedAction == "" {
		return fmt.Errorf("%w: incomplete derived placement revalidation", ErrInvalidRecommendation)
	}
	if result.Status == PlacementAdviceEvidenceCurrent {
		if result.CurrentSnapshotID == "" || result.CurrentSnapshotID != result.SnapshotID ||
			result.Reason != "exact-derived-evidence-current" || result.RecommendedAction != "none" {
			return fmt.Errorf("%w: inconsistent current derived placement revalidation", ErrInvalidRecommendation)
		}
	} else if result.RecommendationReusable {
		return fmt.Errorf("%w: stale derived placement recommendation marked reusable", ErrInvalidRecommendation)
	}
	if placementAdviceDerivedEvidenceRevalidationID(result) != result.RevalidationID {
		return fmt.Errorf("%w: derived placement revalidation integrity mismatch", ErrInvalidRecommendation)
	}
	return nil
}

func placementAdviceDerivedEvidenceRevalidationID(
	result PlacementAdviceDerivedEvidenceRevalidation,
) string {
	canonical := struct {
		SnapshotID                   string                            `json:"snapshot_id"`
		CurrentSnapshotID            string                            `json:"current_snapshot_id,omitempty"`
		PlacementSnapshotID          string                            `json:"placement_snapshot_id"`
		CurrentPlacementSnapshotID   string                            `json:"current_placement_snapshot_id"`
		ProvenanceSnapshotID         string                            `json:"provenance_snapshot_id"`
		CurrentProvenanceSnapshotID  string                            `json:"current_provenance_snapshot_id"`
		DerivationFingerprint        string                            `json:"derivation_fingerprint"`
		CurrentDerivationFingerprint string                            `json:"current_derivation_fingerprint"`
		Status                       PlacementAdviceRevalidationStatus `json:"status"`
		Reason                       string                            `json:"reason"`
		StaleNodeID                  string                            `json:"stale_node_id,omitempty"`
		RecommendationReusable       bool                              `json:"recommendation_reusable"`
		RecommendedAction            string                            `json:"recommended_action"`
	}{
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
		result.StaleNodeID,
		result.RecommendationReusable,
		result.RecommendedAction,
	}
	encoded, _ := json.Marshal(canonical)
	return "padr-" + placementEvidenceSHA256Hex(encoded)[:24]
}
