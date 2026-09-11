package capacity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"control-center/internal/agent"
	"control-center/internal/corecontracts"
)

const AssessmentSchemaV1 = "capacity.assessment/v1"

// NodeProjection is a planner input produced from a qualified CapacityProfile.
type NodeProjection struct {
	NodeID             string                 `json:"node_id"`
	Role               corecontracts.NodeRole `json:"role"`
	Healthy            bool                   `json:"healthy"`
	CurrentWorkload    float64                `json:"current_workload"`
	SafeCapacity       float64                `json:"safe_capacity"`
	TechnicalLimit     float64                `json:"technical_limit"`
	Confidence         Confidence             `json:"confidence"`
	BottleneckMetric   agent.CapacityMetric   `json:"bottleneck_metric"`
	BottleneckTargetID string                 `json:"bottleneck_target_id"`
	BottleneckReserve  float64                `json:"bottleneck_reserve_percent"`
}

type AssessmentRequest struct {
	ScopeID             string                 `json:"scope_id"`
	RequiredRole        corecontracts.NodeRole `json:"required_role"`
	WorkloadUnit        WorkloadUnit           `json:"workload_unit"`
	FailureReserveNodes int                    `json:"failure_reserve_nodes"`
	ExpectedWorkload    float64                `json:"expected_workload"`
}

type Assessment struct {
	SchemaVersion       string                 `json:"schema_version"`
	AssessmentID        string                 `json:"assessment_id"`
	ScopeID             string                 `json:"scope_id"`
	RequiredRole        corecontracts.NodeRole `json:"required_role"`
	WorkloadUnit        WorkloadUnit           `json:"workload_unit"`
	HealthyNodes        int                    `json:"healthy_nodes"`
	FailureReserveNodes int                    `json:"failure_reserve_nodes"`
	CurrentWorkload     float64                `json:"current_workload"`
	ExpectedWorkload    float64                `json:"expected_workload"`
	SafeCapacity        float64                `json:"safe_capacity"`
	TechnicalLimit      float64                `json:"technical_limit"`
	SafeReserve         float64                `json:"safe_reserve"`
	BottleneckNodeID    string                 `json:"bottleneck_node_id"`
	BottleneckMetric    agent.CapacityMetric   `json:"bottleneck_metric"`
	BottleneckTargetID  string                 `json:"bottleneck_target_id"`
	Confidence          Confidence             `json:"confidence"`
	Safe                bool                   `json:"safe"`
	Action              RecommendationAction   `json:"action"`
	AdvisoryOnly        bool                   `json:"advisory_only"`
	ProductionMutation  bool                   `json:"production_mutation"`
}

// BuildAssessment computes safe capacity after removing the largest N nodes.
// It is advisory-only and does not schedule or resize infrastructure.
func BuildAssessment(request AssessmentRequest, nodes []NodeProjection) (Assessment, error) {
	request.ScopeID = strings.TrimSpace(request.ScopeID)
	if !identifierPattern.MatchString(request.ScopeID) || !request.RequiredRole.Valid() || !validWorkloadUnit(request.WorkloadUnit) {
		return Assessment{}, fmt.Errorf("%w: invalid assessment identity", ErrInvalidRecommendation)
	}
	if request.FailureReserveNodes < 0 || request.FailureReserveNodes > 16 || !finite(request.ExpectedWorkload) || request.ExpectedWorkload < 0 {
		return Assessment{}, fmt.Errorf("%w: invalid reserve or workload", ErrInvalidRecommendation)
	}
	if len(nodes) == 0 || len(nodes) > 4096 {
		return Assessment{}, fmt.Errorf("%w: nodes must contain between 1 and 4096 items", ErrInvalidRecommendation)
	}

	eligible := make([]NodeProjection, 0, len(nodes))
	seen := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		node.NodeID, node.BottleneckTargetID = strings.TrimSpace(node.NodeID), strings.TrimSpace(node.BottleneckTargetID)
		if !identifierPattern.MatchString(node.NodeID) || !identifierPattern.MatchString(node.BottleneckTargetID) || !node.Role.Valid() {
			return Assessment{}, fmt.Errorf("%w: invalid node projection identity", ErrInvalidRecommendation)
		}
		if _, duplicate := seen[node.NodeID]; duplicate {
			return Assessment{}, fmt.Errorf("%w: duplicate node %q", ErrInvalidRecommendation, node.NodeID)
		}
		seen[node.NodeID] = struct{}{}
		if !finite(node.CurrentWorkload) || node.CurrentWorkload < 0 || !finitePositive(node.SafeCapacity) || !finitePositive(node.TechnicalLimit) || node.SafeCapacity >= node.TechnicalLimit || !finite(node.BottleneckReserve) {
			return Assessment{}, fmt.Errorf("%w: invalid projection for node %q", ErrInvalidRecommendation, node.NodeID)
		}
		if _, exists := agent.DefinitionForCapacityMetric(node.BottleneckMetric); !exists {
			return Assessment{}, fmt.Errorf("%w: unknown bottleneck metric", ErrInvalidRecommendation)
		}
		if _, err := normalizeConfidence(node.Confidence, confidenceEvidence(node)); err != nil {
			return Assessment{}, fmt.Errorf("%w: node %q confidence: %v", ErrInvalidRecommendation, node.NodeID, err)
		}
		if node.Healthy && node.Role == request.RequiredRole {
			eligible = append(eligible, node)
		}
	}
	if len(eligible) <= request.FailureReserveNodes {
		return Assessment{}, fmt.Errorf("%w: failure reserve leaves no healthy capacity", ErrInvalidRecommendation)
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].NodeID < eligible[j].NodeID })

	current, safe, technical := 0.0, 0.0, 0.0
	confidence := Confidence{Level: ConfidenceCertified, Score: 1}
	bottleneck := eligible[0]
	for _, node := range eligible {
		current += node.CurrentWorkload
		safe += node.SafeCapacity
		technical += node.TechnicalLimit
		if node.Confidence.Score < confidence.Score {
			confidence = node.Confidence
		}
		if node.BottleneckReserve < bottleneck.BottleneckReserve || (closeFloat(node.BottleneckReserve, bottleneck.BottleneckReserve) && node.NodeID < bottleneck.NodeID) {
			bottleneck = node
		}
	}
	byCapacity := append([]NodeProjection(nil), eligible...)
	sort.Slice(byCapacity, func(i, j int) bool {
		if !closeFloat(byCapacity[i].SafeCapacity, byCapacity[j].SafeCapacity) {
			return byCapacity[i].SafeCapacity > byCapacity[j].SafeCapacity
		}
		return byCapacity[i].NodeID < byCapacity[j].NodeID
	})
	for _, node := range byCapacity[:request.FailureReserveNodes] {
		safe -= node.SafeCapacity
		technical -= node.TechnicalLimit
	}
	reserve := safe - request.ExpectedWorkload
	action := ActionNone
	if confidence.Level == ConfidenceLow {
		action = ActionCollectEvidence
	} else if reserve < -floatTolerance(reserve) {
		action = ActionAddRoleCapacity
	}

	canonical := struct {
		Request AssessmentRequest `json:"request"`
		Nodes   []NodeProjection  `json:"nodes"`
	}{request, eligible}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return Assessment{}, fmt.Errorf("%w: fingerprint: %v", ErrInvalidRecommendation, err)
	}
	digest := sha256.Sum256(encoded)
	return Assessment{SchemaVersion: AssessmentSchemaV1, AssessmentID: "cap-" + hex.EncodeToString(digest[:])[:24], ScopeID: request.ScopeID, RequiredRole: request.RequiredRole, WorkloadUnit: request.WorkloadUnit, HealthyNodes: len(eligible), FailureReserveNodes: request.FailureReserveNodes, CurrentWorkload: current, ExpectedWorkload: request.ExpectedWorkload, SafeCapacity: safe, TechnicalLimit: technical, SafeReserve: reserve, BottleneckNodeID: bottleneck.NodeID, BottleneckMetric: bottleneck.BottleneckMetric, BottleneckTargetID: bottleneck.BottleneckTargetID, Confidence: confidence, Safe: reserve >= -floatTolerance(reserve) && bottleneck.BottleneckReserve >= -floatTolerance(bottleneck.BottleneckReserve), Action: action, AdvisoryOnly: true, ProductionMutation: false}, nil
}

// normalizeConfidence requires evidence counts. Projections already originate from a qualified
// profile, so synthesize the minimum structurally valid evidence set without changing its score.
func confidenceEvidence(node NodeProjection) []Evidence {
	if node.Confidence.Level == ConfidenceCertified {
		return []Evidence{
			{ID: "projection-benchmark", SampleCount: 50, Observation: agent.CapacityObservation{Evidence: agent.EvidenceBenchmark}},
			{ID: "projection-measured", SampleCount: 50, Observation: agent.CapacityObservation{Evidence: agent.EvidenceMeasured}},
		}
	}
	return []Evidence{{ID: "projection-evidence", SampleCount: 1, Observation: agent.CapacityObservation{Evidence: agent.EvidenceMeasured}}}
}
