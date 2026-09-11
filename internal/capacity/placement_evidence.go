package capacity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	PlacementAdviceSnapshotSchemaV1     = "capacity.placement-advice-snapshot/v1"
	PlacementAdviceRevalidationSchemaV1 = "capacity.placement-advice-revalidation/v1"
)

type PlacementAdviceRevalidationStatus string

const (
	PlacementAdviceEvidenceCurrent PlacementAdviceRevalidationStatus = "current"
	PlacementAdviceEvidenceStale   PlacementAdviceRevalidationStatus = "stale"
)

type PlacementAdviceSnapshot struct {
	SchemaVersion           string          `json:"schema_version"`
	SnapshotID              string          `json:"snapshot_id"`
	RequestFingerprint      string          `json:"request_fingerprint"`
	NodeEvidenceFingerprint string          `json:"node_evidence_fingerprint"`
	AdviceFingerprint       string          `json:"advice_fingerprint"`
	Advice                  PlacementAdvice `json:"advice"`
	AdvisoryOnly            bool            `json:"advisory_only"`
	ProductionMutation      bool            `json:"production_mutation"`
}

type PlacementAdviceRevalidation struct {
	SchemaVersion            string                            `json:"schema_version"`
	RevalidationID           string                            `json:"revalidation_id"`
	SnapshotID               string                            `json:"snapshot_id"`
	CurrentSnapshotID        string                            `json:"current_snapshot_id"`
	AdviceID                 string                            `json:"advice_id"`
	CurrentAdviceID          string                            `json:"current_advice_id"`
	RecommendedNodeID        string                            `json:"recommended_node_id,omitempty"`
	CurrentRecommendedNodeID string                            `json:"current_recommended_node_id,omitempty"`
	Status                   PlacementAdviceRevalidationStatus `json:"status"`
	Reason                   string                            `json:"reason"`
	RecommendationReusable   bool                              `json:"recommendation_reusable"`
	RecommendedAction        string                            `json:"recommended_action"`
	AdvisoryOnly             bool                              `json:"advisory_only"`
	ProductionMutation       bool                              `json:"production_mutation"`
}

// CapturePlacementAdviceSnapshot seals one placement recommendation to the
// exact request and node projection evidence that produced it. The snapshot is
// advisory evidence only and never authorizes a placement or role mutation.
func CapturePlacementAdviceSnapshot(request PlacementRequest, nodes []NodeProjection) (PlacementAdviceSnapshot, error) {
	advice, err := BuildPlacementAdvice(request, nodes)
	if err != nil {
		return PlacementAdviceSnapshot{}, err
	}
	if advice.SchemaVersion != PlacementAdviceSchemaV1 || !advice.AdvisoryOnly || advice.ProductionMutation {
		return PlacementAdviceSnapshot{}, fmt.Errorf("%w: unsafe placement advice", ErrInvalidRecommendation)
	}

	requestFingerprint, err := placementRequestFingerprint(request)
	if err != nil {
		return PlacementAdviceSnapshot{}, err
	}
	nodeFingerprint, err := placementNodeEvidenceFingerprint(nodes)
	if err != nil {
		return PlacementAdviceSnapshot{}, err
	}
	adviceFingerprint, err := placementAdviceFingerprint(advice)
	if err != nil {
		return PlacementAdviceSnapshot{}, err
	}

	snapshot := PlacementAdviceSnapshot{
		SchemaVersion:           PlacementAdviceSnapshotSchemaV1,
		RequestFingerprint:      requestFingerprint,
		NodeEvidenceFingerprint: nodeFingerprint,
		AdviceFingerprint:       adviceFingerprint,
		Advice:                  advice,
		AdvisoryOnly:            true,
		ProductionMutation:      false,
	}
	snapshot.SnapshotID = placementAdviceSnapshotID(snapshot)
	return snapshot, nil
}

// RevalidatePlacementAdviceSnapshot rebuilds placement advice from current
// bounded inputs. Any request/constraint, telemetry/capacity, or decision drift
// makes the saved recommendation stale. A current result remains advisory and
// does not authorize any placement change.
func RevalidatePlacementAdviceSnapshot(saved PlacementAdviceSnapshot, request PlacementRequest, nodes []NodeProjection) (PlacementAdviceRevalidation, error) {
	if err := validatePlacementAdviceSnapshot(saved); err != nil {
		return PlacementAdviceRevalidation{}, err
	}
	current, err := CapturePlacementAdviceSnapshot(request, nodes)
	if err != nil {
		return PlacementAdviceRevalidation{}, err
	}

	result := PlacementAdviceRevalidation{
		SchemaVersion:            PlacementAdviceRevalidationSchemaV1,
		SnapshotID:               saved.SnapshotID,
		CurrentSnapshotID:        current.SnapshotID,
		AdviceID:                 saved.Advice.AdviceID,
		CurrentAdviceID:          current.Advice.AdviceID,
		RecommendedNodeID:        saved.Advice.RecommendedNodeID,
		CurrentRecommendedNodeID: current.Advice.RecommendedNodeID,
		Status:                   PlacementAdviceEvidenceStale,
		Reason:                   "placement-decision-drift",
		RecommendationReusable:   false,
		RecommendedAction:        "rebuild-placement-advice-from-current-evidence",
		AdvisoryOnly:             true,
		ProductionMutation:       false,
	}

	switch {
	case saved.RequestFingerprint != current.RequestFingerprint:
		result.Reason = "placement-request-or-constraint-drift"
	case saved.NodeEvidenceFingerprint != current.NodeEvidenceFingerprint:
		result.Reason = "node-capacity-or-telemetry-evidence-drift"
	case saved.AdviceFingerprint != current.AdviceFingerprint || saved.Advice.AdviceID != current.Advice.AdviceID:
		result.Reason = "placement-decision-drift"
	case saved.SnapshotID != current.SnapshotID:
		result.Reason = "snapshot-identity-drift"
	default:
		result.Status = PlacementAdviceEvidenceCurrent
		result.Reason = "exact-evidence-current"
		result.RecommendedAction = "none"
		result.RecommendationReusable = reusablePlacementRecommendation(saved.Advice)
	}

	result.RevalidationID = placementAdviceRevalidationID(result)
	return result, nil
}

func validatePlacementAdviceSnapshot(snapshot PlacementAdviceSnapshot) error {
	if snapshot.SchemaVersion != PlacementAdviceSnapshotSchemaV1 || !snapshot.AdvisoryOnly || snapshot.ProductionMutation ||
		snapshot.Advice.SchemaVersion != PlacementAdviceSchemaV1 || !snapshot.Advice.AdvisoryOnly || snapshot.Advice.ProductionMutation {
		return fmt.Errorf("%w: unsafe saved placement advice snapshot", ErrInvalidRecommendation)
	}
	if !validPlacementEvidenceSHA256Hex(snapshot.RequestFingerprint) || !validPlacementEvidenceSHA256Hex(snapshot.NodeEvidenceFingerprint) || !validPlacementEvidenceSHA256Hex(snapshot.AdviceFingerprint) {
		return fmt.Errorf("%w: malformed placement evidence fingerprint", ErrInvalidRecommendation)
	}
	fingerprint, err := placementAdviceFingerprint(snapshot.Advice)
	if err != nil {
		return err
	}
	if fingerprint != snapshot.AdviceFingerprint || placementAdviceSnapshotID(snapshot) != snapshot.SnapshotID {
		return fmt.Errorf("%w: saved placement advice snapshot integrity mismatch", ErrInvalidRecommendation)
	}
	return nil
}

func placementRequestFingerprint(request PlacementRequest) (string, error) {
	request.ScopeID = strings.TrimSpace(request.ScopeID)
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("%w: placement request fingerprint: %v", ErrInvalidRecommendation, err)
	}
	return placementEvidenceSHA256Hex(encoded), nil
}

func placementNodeEvidenceFingerprint(nodes []NodeProjection) (string, error) {
	canonical := append([]NodeProjection(nil), nodes...)
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].NodeID < canonical[j].NodeID })
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("%w: placement node evidence fingerprint: %v", ErrInvalidRecommendation, err)
	}
	return placementEvidenceSHA256Hex(encoded), nil
}

func placementAdviceFingerprint(advice PlacementAdvice) (string, error) {
	encoded, err := json.Marshal(advice)
	if err != nil {
		return "", fmt.Errorf("%w: placement advice fingerprint: %v", ErrInvalidRecommendation, err)
	}
	return placementEvidenceSHA256Hex(encoded), nil
}

func placementAdviceSnapshotID(snapshot PlacementAdviceSnapshot) string {
	canonical := struct {
		RequestFingerprint      string `json:"request_fingerprint"`
		NodeEvidenceFingerprint string `json:"node_evidence_fingerprint"`
		AdviceFingerprint       string `json:"advice_fingerprint"`
	}{snapshot.RequestFingerprint, snapshot.NodeEvidenceFingerprint, snapshot.AdviceFingerprint}
	encoded, _ := json.Marshal(canonical)
	digest := sha256.Sum256(encoded)
	return "pas-" + hex.EncodeToString(digest[:])[:24]
}

func placementAdviceRevalidationID(result PlacementAdviceRevalidation) string {
	canonical := struct {
		SnapshotID        string                            `json:"snapshot_id"`
		CurrentSnapshotID string                            `json:"current_snapshot_id"`
		Status            PlacementAdviceRevalidationStatus `json:"status"`
		Reason            string                            `json:"reason"`
	}{result.SnapshotID, result.CurrentSnapshotID, result.Status, result.Reason}
	encoded, _ := json.Marshal(canonical)
	digest := sha256.Sum256(encoded)
	return "par-" + hex.EncodeToString(digest[:])[:24]
}

func reusablePlacementRecommendation(advice PlacementAdvice) bool {
	return advice.RecommendedNodeID != "" && advice.Action == ActionNone && advice.FleetAssessment.Safe && advice.AdvisoryOnly && !advice.ProductionMutation
}

func validPlacementEvidenceSHA256Hex(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func placementEvidenceSHA256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
