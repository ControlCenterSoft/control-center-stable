package capacity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	PlacementAdviceProvenanceSnapshotSchemaV1     = "capacity.placement-advice-provenance-snapshot/v1"
	PlacementAdviceProvenanceRevalidationSchemaV1 = "capacity.placement-advice-provenance-revalidation/v1"
	maxPlacementTelemetryAgeSeconds               = 7 * 24 * 60 * 60
	maxPlacementOpaqueRevisionLength              = 512
)

// PlacementNodeProvenance binds one NodeProjection to the exact qualified
// CapacityProfile revision and telemetry snapshot revision used to derive it.
// Freshness is evaluated against an explicit caller-supplied evaluation time;
// this contract never reads the host clock.
type PlacementNodeProvenance struct {
	NodeID                 string    `json:"node_id"`
	ProfileObjectID        string    `json:"profile_object_id"`
	ProfileGeneration      uint64    `json:"profile_generation"`
	ProfileResourceVersion string    `json:"profile_resource_version"`
	TelemetrySnapshotID    string    `json:"telemetry_snapshot_id"`
	TelemetryRevision      string    `json:"telemetry_revision"`
	TelemetryObservedAt    time.Time `json:"telemetry_observed_at"`
	TelemetryMaxAgeSeconds uint32    `json:"telemetry_max_age_seconds"`
}

type PlacementAdviceProvenanceSnapshot struct {
	SchemaVersion             string                    `json:"schema_version"`
	ProvenanceSnapshotID      string                    `json:"provenance_snapshot_id"`
	PlacementSnapshotID       string                    `json:"placement_snapshot_id"`
	NodeProvenanceFingerprint string                    `json:"node_provenance_fingerprint"`
	EvaluatedAt               time.Time                 `json:"evaluated_at"`
	Nodes                     []PlacementNodeProvenance `json:"nodes"`
	AdvisoryOnly              bool                      `json:"advisory_only"`
	ProductionMutation        bool                      `json:"production_mutation"`
}

type PlacementAdviceProvenanceRevalidation struct {
	SchemaVersion               string                            `json:"schema_version"`
	RevalidationID              string                            `json:"revalidation_id"`
	ProvenanceSnapshotID        string                            `json:"provenance_snapshot_id"`
	CurrentProvenanceSnapshotID string                            `json:"current_provenance_snapshot_id"`
	PlacementSnapshotID         string                            `json:"placement_snapshot_id"`
	CurrentPlacementSnapshotID  string                            `json:"current_placement_snapshot_id"`
	Status                      PlacementAdviceRevalidationStatus `json:"status"`
	Reason                      string                            `json:"reason"`
	StaleNodeID                 string                            `json:"stale_node_id,omitempty"`
	RecommendationReusable      bool                              `json:"recommendation_reusable"`
	RecommendedAction           string                            `json:"recommended_action"`
	AdvisoryOnly                bool                              `json:"advisory_only"`
	ProductionMutation          bool                              `json:"production_mutation"`
}

// CapturePlacementAdviceProvenanceSnapshot seals placement evidence to exact
// profile and telemetry revisions. evaluatedAt is an explicit deterministic
// input; stale telemetry is rejected and no infrastructure mutation is allowed.
func CapturePlacementAdviceProvenanceSnapshot(
	placement PlacementAdviceSnapshot,
	nodes []NodeProjection,
	provenance []PlacementNodeProvenance,
	evaluatedAt time.Time,
) (PlacementAdviceProvenanceSnapshot, error) {
	return buildPlacementAdviceProvenanceSnapshot(placement, nodes, provenance, evaluatedAt, true)
}

// RevalidatePlacementAdviceProvenanceSnapshot re-evaluates placement and its
// provenance against current bounded inputs. Profile revision changes,
// telemetry revision changes, and telemetry freshness expiry are reported
// separately so the recommendation remains explainable and fail-closed.
func RevalidatePlacementAdviceProvenanceSnapshot(
	saved PlacementAdviceProvenanceSnapshot,
	savedPlacement PlacementAdviceSnapshot,
	request PlacementRequest,
	nodes []NodeProjection,
	provenance []PlacementNodeProvenance,
	evaluatedAt time.Time,
) (PlacementAdviceProvenanceRevalidation, error) {
	if err := validatePlacementAdviceProvenanceSnapshot(saved); err != nil {
		return PlacementAdviceProvenanceRevalidation{}, err
	}
	if err := validatePlacementAdviceSnapshot(savedPlacement); err != nil {
		return PlacementAdviceProvenanceRevalidation{}, err
	}
	if saved.PlacementSnapshotID != savedPlacement.SnapshotID {
		return PlacementAdviceProvenanceRevalidation{}, fmt.Errorf("%w: placement provenance snapshot binding mismatch", ErrInvalidRecommendation)
	}

	placementResult, err := RevalidatePlacementAdviceSnapshot(savedPlacement, request, nodes)
	if err != nil {
		return PlacementAdviceProvenanceRevalidation{}, err
	}
	currentPlacement, err := CapturePlacementAdviceSnapshot(request, nodes)
	if err != nil {
		return PlacementAdviceProvenanceRevalidation{}, err
	}
	current, err := buildPlacementAdviceProvenanceSnapshot(currentPlacement, nodes, provenance, evaluatedAt, false)
	if err != nil {
		return PlacementAdviceProvenanceRevalidation{}, err
	}

	result := PlacementAdviceProvenanceRevalidation{
		SchemaVersion:               PlacementAdviceProvenanceRevalidationSchemaV1,
		ProvenanceSnapshotID:        saved.ProvenanceSnapshotID,
		CurrentProvenanceSnapshotID: current.ProvenanceSnapshotID,
		PlacementSnapshotID:         saved.PlacementSnapshotID,
		CurrentPlacementSnapshotID:  current.PlacementSnapshotID,
		Status:                      PlacementAdviceEvidenceStale,
		Reason:                      "placement-evidence-stale",
		RecommendationReusable:      false,
		RecommendedAction:           "rebuild-placement-advice-from-current-evidence",
		AdvisoryOnly:                true,
		ProductionMutation:          false,
	}

	freshnessReason, freshnessNodeID := placementTelemetryFreshnessIssue(current.Nodes, current.EvaluatedAt)
	switch {
	case placementResult.Status != PlacementAdviceEvidenceCurrent:
		result.Reason = "placement-evidence-stale"
	case freshnessReason != "":
		result.Reason, result.StaleNodeID = freshnessReason, freshnessNodeID
		result.RecommendedAction = "refresh-telemetry-evidence"
	default:
		savedCanonical, err := canonicalPlacementNodeProvenance(saved.Nodes)
		if err != nil {
			return PlacementAdviceProvenanceRevalidation{}, err
		}
		reason, nodeID := placementProvenanceDrift(savedCanonical, current.Nodes)
		if reason != "" {
			result.Reason = reason
			result.StaleNodeID = nodeID
			if reason == "capacity-profile-revision-drift" {
				result.RecommendedAction = "refresh-capacity-profile-evidence"
			} else {
				result.RecommendedAction = "refresh-telemetry-evidence"
			}
		} else {
			result.Status = PlacementAdviceEvidenceCurrent
			result.Reason = "exact-provenance-current"
			result.RecommendationReusable = placementResult.RecommendationReusable
			result.RecommendedAction = "none"
		}
	}

	result.RevalidationID = placementAdviceProvenanceRevalidationID(result)
	return result, nil
}

func buildPlacementAdviceProvenanceSnapshot(
	placement PlacementAdviceSnapshot,
	nodes []NodeProjection,
	provenance []PlacementNodeProvenance,
	evaluatedAt time.Time,
	requireFresh bool,
) (PlacementAdviceProvenanceSnapshot, error) {
	if err := validatePlacementAdviceSnapshot(placement); err != nil {
		return PlacementAdviceProvenanceSnapshot{}, err
	}
	if evaluatedAt.IsZero() {
		return PlacementAdviceProvenanceSnapshot{}, fmt.Errorf("%w: evaluated_at is required", ErrInvalidRecommendation)
	}
	evaluatedAt = evaluatedAt.UTC()
	nodeFingerprint, err := placementNodeEvidenceFingerprint(nodes)
	if err != nil {
		return PlacementAdviceProvenanceSnapshot{}, err
	}
	if placement.NodeEvidenceFingerprint != nodeFingerprint {
		return PlacementAdviceProvenanceSnapshot{}, fmt.Errorf("%w: placement node evidence does not match provenance nodes", ErrInvalidRecommendation)
	}
	canonical, err := canonicalPlacementNodeProvenance(provenance)
	if err != nil {
		return PlacementAdviceProvenanceSnapshot{}, err
	}
	if err := validatePlacementProvenanceCoverage(nodes, canonical); err != nil {
		return PlacementAdviceProvenanceSnapshot{}, err
	}
	if requireFresh {
		if reason, nodeID := placementTelemetryFreshnessIssue(canonical, evaluatedAt); reason != "" {
			return PlacementAdviceProvenanceSnapshot{}, fmt.Errorf("%w: %s for node %q", ErrInvalidRecommendation, reason, nodeID)
		}
	}
	fingerprint, err := placementNodeProvenanceFingerprint(canonical)
	if err != nil {
		return PlacementAdviceProvenanceSnapshot{}, err
	}
	snapshot := PlacementAdviceProvenanceSnapshot{
		SchemaVersion:             PlacementAdviceProvenanceSnapshotSchemaV1,
		PlacementSnapshotID:       placement.SnapshotID,
		NodeProvenanceFingerprint: fingerprint,
		EvaluatedAt:               evaluatedAt,
		Nodes:                     canonical,
		AdvisoryOnly:              true,
		ProductionMutation:        false,
	}
	snapshot.ProvenanceSnapshotID = placementAdviceProvenanceSnapshotID(snapshot)
	return snapshot, nil
}

func validatePlacementAdviceProvenanceSnapshot(snapshot PlacementAdviceProvenanceSnapshot) error {
	if snapshot.SchemaVersion != PlacementAdviceProvenanceSnapshotSchemaV1 || !snapshot.AdvisoryOnly || snapshot.ProductionMutation {
		return fmt.Errorf("%w: unsafe placement provenance snapshot", ErrInvalidRecommendation)
	}
	if snapshot.EvaluatedAt.IsZero() || snapshot.EvaluatedAt.Location() != time.UTC {
		return fmt.Errorf("%w: evaluated_at must be canonical UTC", ErrInvalidRecommendation)
	}
	if snapshot.PlacementSnapshotID == "" || !validPlacementEvidenceSHA256Hex(snapshot.NodeProvenanceFingerprint) {
		return fmt.Errorf("%w: malformed placement provenance identity", ErrInvalidRecommendation)
	}
	canonical, err := canonicalPlacementNodeProvenance(snapshot.Nodes)
	if err != nil {
		return err
	}
	if reason, nodeID := placementTelemetryFreshnessIssue(canonical, snapshot.EvaluatedAt); reason != "" {
		return fmt.Errorf("%w: saved %s for node %q", ErrInvalidRecommendation, reason, nodeID)
	}
	fingerprint, err := placementNodeProvenanceFingerprint(canonical)
	if err != nil {
		return err
	}
	if fingerprint != snapshot.NodeProvenanceFingerprint || placementAdviceProvenanceSnapshotID(snapshot) != snapshot.ProvenanceSnapshotID {
		return fmt.Errorf("%w: placement provenance snapshot integrity mismatch", ErrInvalidRecommendation)
	}
	return nil
}

func canonicalPlacementNodeProvenance(values []PlacementNodeProvenance) ([]PlacementNodeProvenance, error) {
	if len(values) == 0 || len(values) > 4096 {
		return nil, fmt.Errorf("%w: placement provenance must contain between 1 and 4096 nodes", ErrInvalidRecommendation)
	}
	canonical := append([]PlacementNodeProvenance(nil), values...)
	seen := make(map[string]struct{}, len(canonical))
	for index := range canonical {
		item := &canonical[index]
		item.NodeID = strings.TrimSpace(item.NodeID)
		item.ProfileObjectID = strings.TrimSpace(item.ProfileObjectID)
		item.TelemetrySnapshotID = strings.TrimSpace(item.TelemetrySnapshotID)
		item.TelemetryObservedAt = item.TelemetryObservedAt.UTC()
		if !identifierPattern.MatchString(item.NodeID) || !identifierPattern.MatchString(item.ProfileObjectID) || !identifierPattern.MatchString(item.TelemetrySnapshotID) {
			return nil, fmt.Errorf("%w: invalid placement provenance identity", ErrInvalidRecommendation)
		}
		if item.ProfileGeneration == 0 || !validPlacementOpaqueRevision(item.ProfileResourceVersion) || !validPlacementOpaqueRevision(item.TelemetryRevision) {
			return nil, fmt.Errorf("%w: invalid placement provenance revision", ErrInvalidRecommendation)
		}
		if item.TelemetryObservedAt.IsZero() || item.TelemetryMaxAgeSeconds == 0 || item.TelemetryMaxAgeSeconds > maxPlacementTelemetryAgeSeconds {
			return nil, fmt.Errorf("%w: invalid telemetry freshness evidence", ErrInvalidRecommendation)
		}
		if _, duplicate := seen[item.NodeID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate placement provenance for node %q", ErrInvalidRecommendation, item.NodeID)
		}
		seen[item.NodeID] = struct{}{}
	}
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].NodeID < canonical[j].NodeID })
	return canonical, nil
}

func validatePlacementProvenanceCoverage(nodes []NodeProjection, provenance []PlacementNodeProvenance) error {
	if len(nodes) != len(provenance) {
		return fmt.Errorf("%w: placement provenance must cover every node exactly once", ErrInvalidRecommendation)
	}
	nodeIDs := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		nodeID := strings.TrimSpace(node.NodeID)
		if !identifierPattern.MatchString(nodeID) {
			return fmt.Errorf("%w: invalid node identity", ErrInvalidRecommendation)
		}
		if _, duplicate := nodeIDs[nodeID]; duplicate {
			return fmt.Errorf("%w: duplicate node %q", ErrInvalidRecommendation, nodeID)
		}
		nodeIDs[nodeID] = struct{}{}
	}
	for _, item := range provenance {
		if _, exists := nodeIDs[item.NodeID]; !exists {
			return fmt.Errorf("%w: provenance references unknown node %q", ErrInvalidRecommendation, item.NodeID)
		}
	}
	return nil
}

func placementProvenanceDrift(saved, current []PlacementNodeProvenance) (string, string) {
	if len(saved) != len(current) {
		return "node-provenance-set-drift", ""
	}
	for index := range saved {
		left, right := saved[index], current[index]
		if left.NodeID != right.NodeID {
			return "node-provenance-set-drift", right.NodeID
		}
		if left.ProfileObjectID != right.ProfileObjectID || left.ProfileGeneration != right.ProfileGeneration || left.ProfileResourceVersion != right.ProfileResourceVersion {
			return "capacity-profile-revision-drift", right.NodeID
		}
		if left.TelemetrySnapshotID != right.TelemetrySnapshotID || left.TelemetryRevision != right.TelemetryRevision || !left.TelemetryObservedAt.Equal(right.TelemetryObservedAt) || left.TelemetryMaxAgeSeconds != right.TelemetryMaxAgeSeconds {
			return "telemetry-revision-drift", right.NodeID
		}
	}
	return "", ""
}

func placementTelemetryFreshnessIssue(provenance []PlacementNodeProvenance, evaluatedAt time.Time) (string, string) {
	for _, item := range provenance {
		if evaluatedAt.Before(item.TelemetryObservedAt) {
			return "telemetry-observation-after-evaluation", item.NodeID
		}
		maxAge := time.Duration(item.TelemetryMaxAgeSeconds) * time.Second
		if evaluatedAt.Sub(item.TelemetryObservedAt) > maxAge {
			return "telemetry-freshness-expired", item.NodeID
		}
	}
	return "", ""
}

func placementNodeProvenanceFingerprint(values []PlacementNodeProvenance) (string, error) {
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("%w: placement provenance fingerprint: %v", ErrInvalidRecommendation, err)
	}
	return placementEvidenceSHA256Hex(encoded), nil
}

func placementAdviceProvenanceSnapshotID(snapshot PlacementAdviceProvenanceSnapshot) string {
	canonical := struct {
		PlacementSnapshotID       string `json:"placement_snapshot_id"`
		NodeProvenanceFingerprint string `json:"node_provenance_fingerprint"`
		EvaluatedAt               string `json:"evaluated_at"`
	}{snapshot.PlacementSnapshotID, snapshot.NodeProvenanceFingerprint, snapshot.EvaluatedAt.UTC().Format(time.RFC3339Nano)}
	encoded, _ := json.Marshal(canonical)
	digest := sha256.Sum256(encoded)
	return "pps-" + hex.EncodeToString(digest[:])[:24]
}

func placementAdviceProvenanceRevalidationID(result PlacementAdviceProvenanceRevalidation) string {
	canonical := struct {
		ProvenanceSnapshotID        string                            `json:"provenance_snapshot_id"`
		CurrentProvenanceSnapshotID string                            `json:"current_provenance_snapshot_id"`
		Status                      PlacementAdviceRevalidationStatus `json:"status"`
		Reason                      string                            `json:"reason"`
		StaleNodeID                 string                            `json:"stale_node_id,omitempty"`
	}{result.ProvenanceSnapshotID, result.CurrentProvenanceSnapshotID, result.Status, result.Reason, result.StaleNodeID}
	encoded, _ := json.Marshal(canonical)
	digest := sha256.Sum256(encoded)
	return "ppr-" + hex.EncodeToString(digest[:])[:24]
}

func validPlacementOpaqueRevision(value string) bool {
	if value == "" || len(value) > maxPlacementOpaqueRevisionLength || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7e {
			return false
		}
	}
	return true
}
