package capacity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"control-center/internal/agent"
	"control-center/internal/corecontracts"
)

const BottleneckReportSchemaV1 = "capacity.bottleneck-report/v1"

type BottleneckSeverity string

const (
	BottleneckHealthy  BottleneckSeverity = "healthy"
	BottleneckUnknown  BottleneckSeverity = "unknown"
	BottleneckWarning  BottleneckSeverity = "warning"
	BottleneckCritical BottleneckSeverity = "critical"
)

type BottleneckRequest struct {
	ScopeID                string                 `json:"scope_id"`
	RequiredRole           corecontracts.NodeRole `json:"required_role"`
	WorkloadUnit           WorkloadUnit           `json:"workload_unit"`
	FailureReserveNodes    int                    `json:"failure_reserve_nodes"`
	WarningReservePercent  float64                `json:"warning_reserve_percent"`
	CriticalReservePercent float64                `json:"critical_reserve_percent"`
}

type BottleneckFinding struct {
	NodeID                  string               `json:"node_id"`
	Metric                  agent.CapacityMetric `json:"metric"`
	TargetID                string               `json:"target_id"`
	CapacityReservePercent  float64              `json:"capacity_reserve_percent"`
	MetricReservePercent    float64              `json:"metric_reserve_percent"`
	EffectiveReservePercent float64              `json:"effective_reserve_percent"`
	Confidence              Confidence           `json:"confidence"`
	Severity                BottleneckSeverity   `json:"severity"`
	Reason                  string               `json:"reason"`
}

type BottleneckReport struct {
	SchemaVersion      string                 `json:"schema_version"`
	ReportID           string                 `json:"report_id"`
	ScopeID            string                 `json:"scope_id"`
	RequiredRole       corecontracts.NodeRole `json:"required_role"`
	WorkloadUnit       WorkloadUnit           `json:"workload_unit"`
	FleetAssessment    Assessment             `json:"fleet_assessment"`
	Findings           []BottleneckFinding    `json:"findings"`
	CriticalCount      int                    `json:"critical_count"`
	WarningCount       int                    `json:"warning_count"`
	UnknownCount       int                    `json:"unknown_count"`
	Action             RecommendationAction   `json:"action"`
	AdvisoryOnly       bool                   `json:"advisory_only"`
	ProductionMutation bool                   `json:"production_mutation"`
}

// BuildBottleneckReport identifies the narrowest current reserve for every
// healthy node carrying the requested role. It is advisory-only and never
// changes placement, capacity, roles, workloads, or infrastructure.
func BuildBottleneckReport(request BottleneckRequest, nodes []NodeProjection) (BottleneckReport, error) {
	request.ScopeID = strings.TrimSpace(request.ScopeID)
	if !identifierPattern.MatchString(request.ScopeID) || !request.RequiredRole.Valid() || !validWorkloadUnit(request.WorkloadUnit) {
		return BottleneckReport{}, fmt.Errorf("%w: invalid bottleneck identity", ErrInvalidRecommendation)
	}
	if request.FailureReserveNodes < 0 || request.FailureReserveNodes > 16 {
		return BottleneckReport{}, fmt.Errorf("%w: invalid failure reserve", ErrInvalidRecommendation)
	}
	if !finite(request.WarningReservePercent) || !finite(request.CriticalReservePercent) || request.CriticalReservePercent < 0 || request.WarningReservePercent > 90 || request.CriticalReservePercent > request.WarningReservePercent {
		return BottleneckReport{}, fmt.Errorf("%w: reserve thresholds must satisfy 0 <= critical <= warning <= 90", ErrInvalidRecommendation)
	}

	currentWorkload := 0.0
	for _, node := range nodes {
		if node.Healthy && node.Role == request.RequiredRole {
			if !finite(node.CurrentWorkload) || node.CurrentWorkload < 0 {
				return BottleneckReport{}, fmt.Errorf("%w: invalid current workload", ErrInvalidRecommendation)
			}
			currentWorkload += node.CurrentWorkload
			if !finite(currentWorkload) || math.IsInf(currentWorkload, 0) {
				return BottleneckReport{}, fmt.Errorf("%w: current workload overflow", ErrInvalidRecommendation)
			}
		}
	}
	assessment, err := BuildAssessment(AssessmentRequest{
		ScopeID:             request.ScopeID,
		RequiredRole:        request.RequiredRole,
		WorkloadUnit:        request.WorkloadUnit,
		FailureReserveNodes: request.FailureReserveNodes,
		ExpectedWorkload:    currentWorkload,
	}, nodes)
	if err != nil {
		return BottleneckReport{}, fmt.Errorf("fleet assessment: %w", err)
	}

	findings := make([]BottleneckFinding, 0, assessment.HealthyNodes)
	criticalCount, warningCount, unknownCount := 0, 0, 0
	for _, node := range nodes {
		if !node.Healthy || node.Role != request.RequiredRole {
			continue
		}
		capacityReservePercent := (node.SafeCapacity - node.CurrentWorkload) / node.SafeCapacity * 100
		effectiveReservePercent := math.Min(capacityReservePercent, node.BottleneckReserve)
		finding := BottleneckFinding{
			NodeID:                  node.NodeID,
			Metric:                  node.BottleneckMetric,
			TargetID:                node.BottleneckTargetID,
			CapacityReservePercent:  capacityReservePercent,
			MetricReservePercent:    node.BottleneckReserve,
			EffectiveReservePercent: effectiveReservePercent,
			Confidence:              node.Confidence,
			Severity:                BottleneckHealthy,
			Reason:                  "reserve-healthy",
		}
		switch {
		case node.Confidence.Level == ConfidenceLow:
			finding.Severity = BottleneckUnknown
			finding.Reason = "insufficient-confidence"
			unknownCount++
		case effectiveReservePercent <= request.CriticalReservePercent+floatTolerance(effectiveReservePercent, request.CriticalReservePercent):
			finding.Severity = BottleneckCritical
			finding.Reason = limitingReason("critical", capacityReservePercent, node.BottleneckReserve)
			criticalCount++
		case effectiveReservePercent <= request.WarningReservePercent+floatTolerance(effectiveReservePercent, request.WarningReservePercent):
			finding.Severity = BottleneckWarning
			finding.Reason = limitingReason("warning", capacityReservePercent, node.BottleneckReserve)
			warningCount++
		}
		findings = append(findings, finding)
	}

	sort.Slice(findings, func(i, j int) bool {
		left, right := findings[i], findings[j]
		if bottleneckSeverityRank(left.Severity) != bottleneckSeverityRank(right.Severity) {
			return bottleneckSeverityRank(left.Severity) < bottleneckSeverityRank(right.Severity)
		}
		if !closeFloat(left.EffectiveReservePercent, right.EffectiveReservePercent) {
			return left.EffectiveReservePercent < right.EffectiveReservePercent
		}
		return left.NodeID < right.NodeID
	})

	action := ActionNone
	switch {
	case !assessment.Safe || criticalCount > 0:
		action = ActionAddRoleCapacity
	case unknownCount > 0:
		action = ActionCollectEvidence
	case warningCount > 0:
		action = ActionAdjustWorkloadPolicy
	}

	canonical := struct {
		Request BottleneckRequest `json:"request"`
		Nodes   []NodeProjection  `json:"nodes"`
	}{request, append([]NodeProjection(nil), nodes...)}
	sort.Slice(canonical.Nodes, func(i, j int) bool { return canonical.Nodes[i].NodeID < canonical.Nodes[j].NodeID })
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return BottleneckReport{}, fmt.Errorf("%w: bottleneck fingerprint: %v", ErrInvalidRecommendation, err)
	}
	digest := sha256.Sum256(encoded)
	return BottleneckReport{
		SchemaVersion:      BottleneckReportSchemaV1,
		ReportID:           "br-" + hex.EncodeToString(digest[:])[:24],
		ScopeID:            request.ScopeID,
		RequiredRole:       request.RequiredRole,
		WorkloadUnit:       request.WorkloadUnit,
		FleetAssessment:    assessment,
		Findings:           findings,
		CriticalCount:      criticalCount,
		WarningCount:       warningCount,
		UnknownCount:       unknownCount,
		Action:             action,
		AdvisoryOnly:       true,
		ProductionMutation: false,
	}, nil
}

func limitingReason(level string, capacityReservePercent, metricReservePercent float64) string {
	if capacityReservePercent <= metricReservePercent+floatTolerance(capacityReservePercent, metricReservePercent) {
		return "capacity-reserve-" + level
	}
	return "metric-reserve-" + level
}

func bottleneckSeverityRank(severity BottleneckSeverity) int {
	switch severity {
	case BottleneckCritical:
		return 0
	case BottleneckWarning:
		return 1
	case BottleneckUnknown:
		return 2
	default:
		return 3
	}
}
