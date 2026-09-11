package capacity

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	PlacementAdviceDerivationProvenanceSchemaV1 = "capacity.placement-advice-derivation-provenance/v1"
)

type PlacementNodeDerivationInput struct {
	Evidence  NodeProjectionDerivationEvidence `json:"evidence"`
	Profile   Profile                          `json:"profile"`
	Telemetry NodeProjectionTelemetry          `json:"telemetry"`
}

type PlacementNodeDerivationBinding struct {
	NodeID       string `json:"node_id"`
	DerivationID string `json:"derivation_id"`
}

type PlacementAdviceDerivationProvenanceSnapshot struct {
	SchemaVersion         string                            `json:"schema_version"`
	SnapshotID            string                            `json:"snapshot_id"`
	Provenance            PlacementAdviceProvenanceSnapshot `json:"provenance"`
	DerivationFingerprint string                            `json:"derivation_fingerprint"`
	Nodes                 []PlacementNodeDerivationBinding  `json:"nodes"`
	AdvisoryOnly          bool                              `json:"advisory_only"`
	ProductionMutation    bool                              `json:"production_mutation"`
}

// CapturePlacementAdviceDerivationProvenance validates each projection against
// its exact profile and telemetry inputs before allowing it to participate in
// placement provenance. This prevents callers from asserting provenance for a
// projection that was not actually derived from the supplied evidence.
func CapturePlacementAdviceDerivationProvenance(
	placement PlacementAdviceSnapshot,
	inputs []PlacementNodeDerivationInput,
	evaluatedAt time.Time,
) (PlacementAdviceDerivationProvenanceSnapshot, error) {
	nodes, provenance, bindings, err := validatePlacementNodeDerivations(inputs, evaluatedAt)
	if err != nil {
		return PlacementAdviceDerivationProvenanceSnapshot{}, err
	}
	sealed, err := CapturePlacementAdviceProvenanceSnapshot(placement, nodes, provenance, evaluatedAt)
	if err != nil {
		return PlacementAdviceDerivationProvenanceSnapshot{}, err
	}
	fingerprint, err := placementNodeDerivationFingerprint(bindings)
	if err != nil {
		return PlacementAdviceDerivationProvenanceSnapshot{}, err
	}
	snapshot := PlacementAdviceDerivationProvenanceSnapshot{
		SchemaVersion:         PlacementAdviceDerivationProvenanceSchemaV1,
		Provenance:            sealed,
		DerivationFingerprint: fingerprint,
		Nodes:                 bindings,
		AdvisoryOnly:          true,
		ProductionMutation:    false,
	}
	snapshot.SnapshotID = placementAdviceDerivationProvenanceID(snapshot)
	return snapshot, nil
}

// RevalidatePlacementAdviceDerivationProvenance revalidates both the existing
// placement provenance and the exact node-projection derivations. A changed
// derivation is fail-closed even when it happens to produce the same projection
// and reuses the same profile/telemetry revision metadata.
func RevalidatePlacementAdviceDerivationProvenance(
	saved PlacementAdviceDerivationProvenanceSnapshot,
	savedPlacement PlacementAdviceSnapshot,
	request PlacementRequest,
	inputs []PlacementNodeDerivationInput,
	evaluatedAt time.Time,
) (PlacementAdviceProvenanceRevalidation, error) {
	if err := validatePlacementAdviceDerivationProvenanceSnapshot(saved); err != nil {
		return PlacementAdviceProvenanceRevalidation{}, err
	}
	nodes, provenance, bindings, err := validatePlacementNodeDerivations(inputs, evaluatedAt)
	if err != nil {
		return PlacementAdviceProvenanceRevalidation{}, err
	}
	result, err := RevalidatePlacementAdviceProvenanceSnapshot(
		saved.Provenance,
		savedPlacement,
		request,
		nodes,
		provenance,
		evaluatedAt,
	)
	if err != nil {
		return PlacementAdviceProvenanceRevalidation{}, err
	}
	if result.Status == PlacementAdviceEvidenceCurrent {
		fingerprint, err := placementNodeDerivationFingerprint(bindings)
		if err != nil {
			return PlacementAdviceProvenanceRevalidation{}, err
		}
		if fingerprint != saved.DerivationFingerprint {
			result.Status = PlacementAdviceEvidenceStale
			result.Reason = "projection-derivation-drift"
			result.StaleNodeID = placementNodeDerivationDrift(saved.Nodes, bindings)
			result.RecommendationReusable = false
			result.RecommendedAction = "rebuild-node-projections-from-current-evidence"
			result.RevalidationID = placementAdviceProvenanceRevalidationID(result)
		}
	}
	return result, nil
}

func validatePlacementNodeDerivations(
	inputs []PlacementNodeDerivationInput,
	evaluatedAt time.Time,
) ([]NodeProjection, []PlacementNodeProvenance, []PlacementNodeDerivationBinding, error) {
	if len(inputs) == 0 || len(inputs) > 4096 {
		return nil, nil, nil, fmt.Errorf(
			"%w: placement derivations must contain between 1 and 4096 nodes",
			ErrInvalidRecommendation,
		)
	}
	if evaluatedAt.IsZero() {
		return nil, nil, nil, fmt.Errorf("%w: evaluated_at is required", ErrInvalidRecommendation)
	}
	evaluatedAt = evaluatedAt.UTC()
	nodes := make([]NodeProjection, 0, len(inputs))
	provenance := make([]PlacementNodeProvenance, 0, len(inputs))
	bindings := make([]PlacementNodeDerivationBinding, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		evidence := input.Evidence
		if evidence.EvaluatedAt.IsZero() || evaluatedAt.Before(evidence.EvaluatedAt) {
			return nil, nil, nil, fmt.Errorf(
				"%w: projection derivation is from the future",
				ErrInvalidRecommendation,
			)
		}
		if err := ValidateNodeProjectionDerivation(
			evidence,
			input.Profile,
			input.Telemetry,
			evidence.EvaluatedAt,
		); err != nil {
			return nil, nil, nil, fmt.Errorf("projection derivation %q: %w", evidence.DerivationID, err)
		}
		nodeID := strings.TrimSpace(evidence.Projection.NodeID)
		if _, duplicate := seen[nodeID]; duplicate {
			return nil, nil, nil, fmt.Errorf(
				"%w: duplicate projection derivation for node %q",
				ErrInvalidRecommendation,
				nodeID,
			)
		}
		seen[nodeID] = struct{}{}
		nodes = append(nodes, evidence.Projection)
		provenance = append(provenance, PlacementNodeProvenance{
			NodeID:                 nodeID,
			ProfileObjectID:        evidence.ProfileObjectID,
			ProfileGeneration:      evidence.ProfileGeneration,
			ProfileResourceVersion: evidence.ProfileResourceVersion,
			TelemetrySnapshotID:    evidence.TelemetrySnapshotID,
			TelemetryRevision:      evidence.TelemetryRevision,
			TelemetryObservedAt:    evidence.TelemetryObservedAt,
			TelemetryMaxAgeSeconds: maxProjectionTelemetryAgeSeconds,
		})
		bindings = append(bindings, PlacementNodeDerivationBinding{
			NodeID:       nodeID,
			DerivationID: evidence.DerivationID,
		})
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].NodeID < bindings[j].NodeID })
	return nodes, provenance, bindings, nil
}

func validatePlacementAdviceDerivationProvenanceSnapshot(
	snapshot PlacementAdviceDerivationProvenanceSnapshot,
) error {
	if snapshot.SchemaVersion != PlacementAdviceDerivationProvenanceSchemaV1 ||
		!snapshot.AdvisoryOnly || snapshot.ProductionMutation {
		return fmt.Errorf("%w: unsafe placement derivation provenance snapshot", ErrInvalidRecommendation)
	}
	if err := validatePlacementAdviceProvenanceSnapshot(snapshot.Provenance); err != nil {
		return err
	}
	bindings, err := canonicalPlacementNodeDerivationBindings(snapshot.Nodes)
	if err != nil {
		return err
	}
	fingerprint, err := placementNodeDerivationFingerprint(bindings)
	if err != nil {
		return err
	}
	if fingerprint != snapshot.DerivationFingerprint ||
		placementAdviceDerivationProvenanceID(snapshot) != snapshot.SnapshotID {
		return fmt.Errorf(
			"%w: placement derivation provenance integrity mismatch",
			ErrInvalidRecommendation,
		)
	}
	return nil
}

func canonicalPlacementNodeDerivationBindings(
	values []PlacementNodeDerivationBinding,
) ([]PlacementNodeDerivationBinding, error) {
	if len(values) == 0 || len(values) > 4096 {
		return nil, fmt.Errorf("%w: invalid placement derivation binding set", ErrInvalidRecommendation)
	}
	canonical := append([]PlacementNodeDerivationBinding(nil), values...)
	seen := make(map[string]struct{}, len(canonical))
	for index := range canonical {
		canonical[index].NodeID = strings.TrimSpace(canonical[index].NodeID)
		canonical[index].DerivationID = strings.TrimSpace(canonical[index].DerivationID)
		if !identifierPattern.MatchString(canonical[index].NodeID) ||
			!strings.HasPrefix(canonical[index].DerivationID, "npd-") ||
			len(canonical[index].DerivationID) != len("npd-")+24 {
			return nil, fmt.Errorf("%w: invalid placement derivation binding", ErrInvalidRecommendation)
		}
		if _, duplicate := seen[canonical[index].NodeID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate placement derivation binding", ErrInvalidRecommendation)
		}
		seen[canonical[index].NodeID] = struct{}{}
	}
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].NodeID < canonical[j].NodeID })
	return canonical, nil
}

func placementNodeDerivationFingerprint(values []PlacementNodeDerivationBinding) (string, error) {
	canonical, err := canonicalPlacementNodeDerivationBindings(values)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("%w: placement derivation fingerprint: %v", ErrInvalidRecommendation, err)
	}
	return placementEvidenceSHA256Hex(encoded), nil
}

func placementAdviceDerivationProvenanceID(
	snapshot PlacementAdviceDerivationProvenanceSnapshot,
) string {
	canonical := struct {
		ProvenanceSnapshotID  string `json:"provenance_snapshot_id"`
		DerivationFingerprint string `json:"derivation_fingerprint"`
	}{snapshot.Provenance.ProvenanceSnapshotID, snapshot.DerivationFingerprint}
	encoded, _ := json.Marshal(canonical)
	return "padp-" + placementEvidenceSHA256Hex(encoded)[:24]
}

func placementNodeDerivationDrift(saved, current []PlacementNodeDerivationBinding) string {
	left, err := canonicalPlacementNodeDerivationBindings(saved)
	if err != nil {
		return ""
	}
	right, err := canonicalPlacementNodeDerivationBindings(current)
	if err != nil {
		return ""
	}
	if len(left) != len(right) {
		return ""
	}
	for index := range left {
		if left[index].NodeID != right[index].NodeID {
			return right[index].NodeID
		}
		if left[index].DerivationID != right[index].DerivationID {
			return right[index].NodeID
		}
	}
	return ""
}
