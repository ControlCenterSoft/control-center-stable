package capacity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"control-center/internal/corecontracts"
)

const PlacementAdviceSchemaV1 = "capacity.placement-advice/v1"

type PlacementRequest struct {
	ScopeID                   string                 `json:"scope_id"`
	RequiredRole              corecontracts.NodeRole `json:"required_role"`
	WorkloadUnit              WorkloadUnit           `json:"workload_unit"`
	IncrementalWorkload       float64                `json:"incremental_workload"`
	FailureReserveNodes       int                    `json:"failure_reserve_nodes"`
	MinimumNodeReservePercent float64                `json:"minimum_node_reserve_percent"`
}

type PlacementCandidate struct {
	NodeID                   string     `json:"node_id"`
	Eligible                 bool       `json:"eligible"`
	Reason                   string     `json:"reason"`
	CurrentWorkload          float64    `json:"current_workload"`
	ProjectedWorkload        float64    `json:"projected_workload"`
	SafeCapacity             float64    `json:"safe_capacity"`
	TechnicalLimit           float64    `json:"technical_limit"`
	SafeReserve              float64    `json:"safe_reserve"`
	SafeReservePercent       float64    `json:"safe_reserve_percent"`
	TechnicalReserve         float64    `json:"technical_reserve"`
	BottleneckReservePercent float64    `json:"bottleneck_reserve_percent"`
	Confidence               Confidence `json:"confidence"`
}

type PlacementAdvice struct {
	SchemaVersion       string                 `json:"schema_version"`
	AdviceID            string                 `json:"advice_id"`
	ScopeID             string                 `json:"scope_id"`
	RequiredRole        corecontracts.NodeRole `json:"required_role"`
	WorkloadUnit        WorkloadUnit           `json:"workload_unit"`
	IncrementalWorkload float64                `json:"incremental_workload"`
	FleetAssessment     Assessment             `json:"fleet_assessment"`
	Candidates          []PlacementCandidate   `json:"candidates"`
	RecommendedNodeID   string                 `json:"recommended_node_id,omitempty"`
	Action              RecommendationAction   `json:"action"`
	AdvisoryOnly        bool                   `json:"advisory_only"`
	ProductionMutation  bool                   `json:"production_mutation"`
}

// BuildPlacementAdvice ranks healthy nodes that already carry the requested
// role. The function never changes role assignments or infrastructure. Fleet
// safety is evaluated with the requested failure reserve before a recommendation
// is emitted.
func BuildPlacementAdvice(request PlacementRequest, nodes []NodeProjection) (PlacementAdvice, error) {
	request.ScopeID = strings.TrimSpace(request.ScopeID)
	if !identifierPattern.MatchString(request.ScopeID) || !request.RequiredRole.Valid() || !validWorkloadUnit(request.WorkloadUnit) {
		return PlacementAdvice{}, fmt.Errorf("%w: invalid placement identity", ErrInvalidRecommendation)
	}
	if !finitePositive(request.IncrementalWorkload) {
		return PlacementAdvice{}, fmt.Errorf("%w: incremental_workload must be positive", ErrInvalidRecommendation)
	}
	if request.FailureReserveNodes < 0 || request.FailureReserveNodes > 16 {
		return PlacementAdvice{}, fmt.Errorf("%w: invalid failure reserve", ErrInvalidRecommendation)
	}
	if !finite(request.MinimumNodeReservePercent) || request.MinimumNodeReservePercent < 0 || request.MinimumNodeReservePercent > 90 {
		return PlacementAdvice{}, fmt.Errorf("%w: minimum_node_reserve_percent must be within [0,90]", ErrInvalidRecommendation)
	}

	currentFleetWorkload := 0.0
	for _, node := range nodes {
		if node.Healthy && node.Role == request.RequiredRole {
			currentFleetWorkload += node.CurrentWorkload
		}
	}
	fleetAssessment, err := BuildAssessment(AssessmentRequest{
		ScopeID:             request.ScopeID,
		RequiredRole:        request.RequiredRole,
		WorkloadUnit:        request.WorkloadUnit,
		FailureReserveNodes: request.FailureReserveNodes,
		ExpectedWorkload:    currentFleetWorkload + request.IncrementalWorkload,
	}, nodes)
	if err != nil {
		return PlacementAdvice{}, fmt.Errorf("fleet assessment: %w", err)
	}

	candidates := make([]PlacementCandidate, 0, len(nodes))
	for _, node := range nodes {
		if !node.Healthy || node.Role != request.RequiredRole {
			continue
		}
		projected := node.CurrentWorkload + request.IncrementalWorkload
		safeReserve := node.SafeCapacity - projected
		technicalReserve := node.TechnicalLimit - projected
		safeReservePercent := safeReserve / node.SafeCapacity * 100
		candidate := PlacementCandidate{
			NodeID:                   node.NodeID,
			CurrentWorkload:          node.CurrentWorkload,
			ProjectedWorkload:        projected,
			SafeCapacity:             node.SafeCapacity,
			TechnicalLimit:           node.TechnicalLimit,
			SafeReserve:              safeReserve,
			SafeReservePercent:       safeReservePercent,
			TechnicalReserve:         technicalReserve,
			BottleneckReservePercent: node.BottleneckReserve,
			Confidence:               node.Confidence,
		}
		switch {
		case node.Confidence.Level == ConfidenceLow:
			candidate.Reason = "insufficient-confidence"
		case safeReserve < -floatTolerance(safeReserve):
			candidate.Reason = "safe-capacity-exceeded"
		case technicalReserve < -floatTolerance(technicalReserve):
			candidate.Reason = "technical-limit-exceeded"
		case node.BottleneckReserve < -floatTolerance(node.BottleneckReserve):
			candidate.Reason = "existing-bottleneck-overload"
		case safeReservePercent+floatTolerance(safeReservePercent, request.MinimumNodeReservePercent) < request.MinimumNodeReservePercent:
			candidate.Reason = "minimum-node-reserve-not-preserved"
		default:
			candidate.Eligible = true
			candidate.Reason = "eligible"
		}
		candidates = append(candidates, candidate)
	}
	if len(candidates) == 0 {
		return PlacementAdvice{}, fmt.Errorf("%w: no healthy nodes for required role", ErrInvalidRecommendation)
	}

	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.Eligible != right.Eligible {
			return left.Eligible
		}
		if !closeFloat(left.SafeReservePercent, right.SafeReservePercent) {
			return left.SafeReservePercent > right.SafeReservePercent
		}
		if !closeFloat(left.Confidence.Score, right.Confidence.Score) {
			return left.Confidence.Score > right.Confidence.Score
		}
		if !closeFloat(left.BottleneckReservePercent, right.BottleneckReservePercent) {
			return left.BottleneckReservePercent > right.BottleneckReservePercent
		}
		return left.NodeID < right.NodeID
	})

	recommended := ""
	action := ActionNone
	if !fleetAssessment.Safe {
		action = ActionAddRoleCapacity
	} else if candidates[0].Eligible {
		recommended = candidates[0].NodeID
	} else {
		action = ActionAddRoleCapacity
		for _, candidate := range candidates {
			if candidate.Reason == "insufficient-confidence" {
				action = ActionCollectEvidence
				break
			}
		}
	}

	canonical := struct {
		Request PlacementRequest `json:"request"`
		Nodes   []NodeProjection `json:"nodes"`
	}{request, append([]NodeProjection(nil), nodes...)}
	sort.Slice(canonical.Nodes, func(i, j int) bool { return canonical.Nodes[i].NodeID < canonical.Nodes[j].NodeID })
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return PlacementAdvice{}, fmt.Errorf("%w: placement fingerprint: %v", ErrInvalidRecommendation, err)
	}
	digest := sha256.Sum256(encoded)
	return PlacementAdvice{
		SchemaVersion:       PlacementAdviceSchemaV1,
		AdviceID:            "pa-" + hex.EncodeToString(digest[:])[:24],
		ScopeID:             request.ScopeID,
		RequiredRole:        request.RequiredRole,
		WorkloadUnit:        request.WorkloadUnit,
		IncrementalWorkload: request.IncrementalWorkload,
		FleetAssessment:     fleetAssessment,
		Candidates:          candidates,
		RecommendedNodeID:   recommended,
		Action:              action,
		AdvisoryOnly:        true,
		ProductionMutation:  false,
	}, nil
}
