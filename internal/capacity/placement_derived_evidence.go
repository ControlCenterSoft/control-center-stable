package capacity

import (
	"encoding/json"
	"fmt"
	"time"
)

const PlacementAdviceDerivedEvidenceSchemaV1 = "capacity.placement-advice-derived-evidence/v1"

// PlacementAdviceDerivedEvidenceSnapshot is the strong planner recommendation
// envelope. It can only be built from projection derivation inputs that are
// revalidated against their exact CapacityProfile and telemetry evidence.
// The envelope is advisory evidence and never authorizes infrastructure change.
type PlacementAdviceDerivedEvidenceSnapshot struct {
	SchemaVersion        string                                      `json:"schema_version"`
	SnapshotID           string                                      `json:"snapshot_id"`
	Placement            PlacementAdviceSnapshot                     `json:"placement"`
	DerivationProvenance PlacementAdviceDerivationProvenanceSnapshot `json:"derivation_provenance"`
	AdvisoryOnly         bool                                        `json:"advisory_only"`
	ProductionMutation   bool                                        `json:"production_mutation"`
}

// CapturePlacementAdviceFromDerivations builds placement advice only after each
// caller-supplied profile/telemetry/evidence tuple has been revalidated. Callers
// cannot inject an arbitrary []NodeProjection into this stronger contract.
func CapturePlacementAdviceFromDerivations(
	request PlacementRequest,
	inputs []PlacementNodeDerivationInput,
	evaluatedAt time.Time,
) (PlacementAdviceDerivedEvidenceSnapshot, error) {
	nodes, _, _, err := validatePlacementNodeDerivations(inputs, evaluatedAt)
	if err != nil {
		return PlacementAdviceDerivedEvidenceSnapshot{}, err
	}
	placement, err := CapturePlacementAdviceSnapshot(request, nodes)
	if err != nil {
		return PlacementAdviceDerivedEvidenceSnapshot{}, err
	}
	provenance, err := CapturePlacementAdviceDerivationProvenance(
		placement,
		inputs,
		evaluatedAt,
	)
	if err != nil {
		return PlacementAdviceDerivedEvidenceSnapshot{}, err
	}

	snapshot := PlacementAdviceDerivedEvidenceSnapshot{
		SchemaVersion:        PlacementAdviceDerivedEvidenceSchemaV1,
		Placement:            placement,
		DerivationProvenance: provenance,
		AdvisoryOnly:         true,
		ProductionMutation:   false,
	}
	snapshot.SnapshotID = placementAdviceDerivedEvidenceID(snapshot)
	if err := validatePlacementAdviceDerivedEvidenceSnapshot(snapshot); err != nil {
		return PlacementAdviceDerivedEvidenceSnapshot{}, err
	}
	return snapshot, nil
}

// ValidatePlacementAdviceDerivedEvidence verifies envelope integrity and the
// exact nested placement/provenance binding. It does not re-read telemetry;
// current-evidence revalidation remains a separate explicit planner operation.
func ValidatePlacementAdviceDerivedEvidence(snapshot PlacementAdviceDerivedEvidenceSnapshot) error {
	return validatePlacementAdviceDerivedEvidenceSnapshot(snapshot)
}

func validatePlacementAdviceDerivedEvidenceSnapshot(snapshot PlacementAdviceDerivedEvidenceSnapshot) error {
	if snapshot.SchemaVersion != PlacementAdviceDerivedEvidenceSchemaV1 ||
		!snapshot.AdvisoryOnly || snapshot.ProductionMutation {
		return fmt.Errorf("%w: unsafe derived placement evidence", ErrInvalidRecommendation)
	}
	if err := validatePlacementAdviceSnapshot(snapshot.Placement); err != nil {
		return err
	}
	if err := validatePlacementAdviceDerivationProvenanceSnapshot(snapshot.DerivationProvenance); err != nil {
		return err
	}
	if snapshot.DerivationProvenance.Provenance.PlacementSnapshotID != snapshot.Placement.SnapshotID {
		return fmt.Errorf("%w: derived placement provenance binding mismatch", ErrInvalidRecommendation)
	}
	if snapshot.SnapshotID == "" || placementAdviceDerivedEvidenceID(snapshot) != snapshot.SnapshotID {
		return fmt.Errorf("%w: derived placement evidence integrity mismatch", ErrInvalidRecommendation)
	}
	return nil
}

func placementAdviceDerivedEvidenceID(snapshot PlacementAdviceDerivedEvidenceSnapshot) string {
	canonical := struct {
		PlacementSnapshotID   string `json:"placement_snapshot_id"`
		DerivationSnapshotID  string `json:"derivation_snapshot_id"`
		DerivationFingerprint string `json:"derivation_fingerprint"`
	}{
		snapshot.Placement.SnapshotID,
		snapshot.DerivationProvenance.SnapshotID,
		snapshot.DerivationProvenance.DerivationFingerprint,
	}
	encoded, _ := json.Marshal(canonical)
	return "pade-" + placementEvidenceSHA256Hex(encoded)[:24]
}
