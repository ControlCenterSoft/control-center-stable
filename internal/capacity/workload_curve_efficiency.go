package capacity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
)

const WorkloadCurveEfficiencySchemaV1 = "capacity.workload-curve-efficiency/v1"

type WorkloadCurveEfficiencyStatus string

const (
	WorkloadCurveEfficiencyEfficient          WorkloadCurveEfficiencyStatus = "efficient"
	WorkloadCurveEfficiencyDiminishingReturns WorkloadCurveEfficiencyStatus = "diminishing-returns"
	WorkloadCurveEfficiencyCollectEvidence    WorkloadCurveEfficiencyStatus = "collect-evidence"
	WorkloadCurveEfficiencyBlocked            WorkloadCurveEfficiencyStatus = "blocked"
)

type WorkloadCurveEfficiencyPolicy struct {
	MinimumEfficiencyRatio float64                   `json:"minimum_efficiency_ratio"`
	MinimumConfidence      WorkloadProfileConfidence `json:"minimum_confidence"`
}

type WorkloadCurveEfficiencySegment struct {
	LeftPointID          string  `json:"left_point_id"`
	RightPointID         string  `json:"right_point_id"`
	ResourceFactorStart  float64 `json:"resource_factor_start"`
	ResourceFactorEnd    float64 `json:"resource_factor_end"`
	SafeWorkloadStart    float64 `json:"safe_workload_start"`
	SafeWorkloadEnd      float64 `json:"safe_workload_end"`
	MarginalWorkloadGain float64 `json:"marginal_workload_gain"`
	EfficiencyRatio      float64 `json:"efficiency_ratio"`
}

type WorkloadCurveEfficiencyReport struct {
	SchemaVersion      string                           `json:"schema_version"`
	ReportID           string                           `json:"report_id"`
	CurveID            string                           `json:"curve_id"`
	ScopeID            string                           `json:"scope_id"`
	WorkloadUnit       WorkloadUnit                     `json:"workload_unit"`
	Policy             WorkloadCurveEfficiencyPolicy    `json:"policy"`
	Segments           []WorkloadCurveEfficiencySegment `json:"segments"`
	LowestEfficiency   float64                          `json:"lowest_efficiency"`
	Status             WorkloadCurveEfficiencyStatus    `json:"status"`
	Reason             string                           `json:"reason"`
	RecommendedAction  string                           `json:"recommended_action"`
	Confidence         WorkloadProfileConfidence        `json:"confidence"`
	AdvisoryOnly       bool                             `json:"advisory_only"`
	ProductionMutation bool                             `json:"production_mutation"`
}

// AnalyzeWorkloadCurveEfficiency measures marginal gain only inside an exact,
// benchmark-backed curve. It identifies diminishing returns but never converts
// the result into scaling or placement authority.
func AnalyzeWorkloadCurveEfficiency(curve WorkloadCurve, policy WorkloadCurveEfficiencyPolicy) (WorkloadCurveEfficiencyReport, error) {
	if curve.SchemaVersion != WorkloadCurveSchemaV1 || !curve.AdvisoryOnly || curve.ProductionMutation {
		return WorkloadCurveEfficiencyReport{}, fmt.Errorf("%w: invalid workload curve evidence", ErrInvalidRecommendation)
	}
	if math.IsNaN(policy.MinimumEfficiencyRatio) || math.IsInf(policy.MinimumEfficiencyRatio, 0) ||
		policy.MinimumEfficiencyRatio <= 0 || policy.MinimumEfficiencyRatio > 1 {
		return WorkloadCurveEfficiencyReport{}, fmt.Errorf("%w: minimum_efficiency_ratio must be within (0,1]", ErrInvalidRecommendation)
	}
	if !validWorkloadProfileConfidence(policy.MinimumConfidence) {
		return WorkloadCurveEfficiencyReport{}, fmt.Errorf("%w: invalid minimum confidence", ErrInvalidRecommendation)
	}
	if curve.Status == WorkloadCurveReady && len(curve.Points) < 2 {
		return WorkloadCurveEfficiencyReport{}, fmt.Errorf("%w: ready workload curve requires at least two points", ErrInvalidRecommendation)
	}

	rebuilt, err := BuildWorkloadCurve(
		WorkloadCurveRequest{ScopeID: curve.ScopeID, WorkloadUnit: curve.WorkloadUnit, MinimumPoints: curve.MinimumPoints},
		curve.Points,
	)
	if err != nil {
		return WorkloadCurveEfficiencyReport{}, fmt.Errorf("%w: rebuild workload curve: %v", ErrInvalidRecommendation, err)
	}
	if rebuilt.CurveID != curve.CurveID || rebuilt.Status != curve.Status || rebuilt.Reason != curve.Reason ||
		rebuilt.Confidence != curve.Confidence {
		return WorkloadCurveEfficiencyReport{}, fmt.Errorf("%w: workload curve evidence mismatch", ErrInvalidRecommendation)
	}
	curve = rebuilt

	status := WorkloadCurveEfficiencyEfficient
	reason := "marginal_efficiency_within_policy"
	action := "none"
	if curve.Status == WorkloadCurveBlocked {
		status = WorkloadCurveEfficiencyBlocked
		reason = "curve_blocked"
		action = "collect-evidence"
	}
	if workloadProfileConfidenceRank(curve.Confidence) < workloadProfileConfidenceRank(policy.MinimumConfidence) {
		status = WorkloadCurveEfficiencyCollectEvidence
		reason = "curve_confidence_below_policy"
		action = "collect-evidence"
	}

	segments := make([]WorkloadCurveEfficiencySegment, 0, len(curve.Points)-1)
	lowest := 1.0
	if curve.Status == WorkloadCurveReady {
		if len(curve.Points) < 2 {
			return WorkloadCurveEfficiencyReport{}, fmt.Errorf("%w: rebuilt ready workload curve requires at least two points", ErrInvalidRecommendation)
		}
		firstLeft := curve.Points[0]
		firstRight := curve.Points[1]
		baselineDeltaResource := firstRight.ResourceFactor - firstLeft.ResourceFactor
		if baselineDeltaResource <= 0 {
			return WorkloadCurveEfficiencyReport{}, fmt.Errorf("%w: baseline segment resource factors must increase", ErrInvalidRecommendation)
		}
		baselineGain := firstRight.SafeWorkload - firstLeft.SafeWorkload
		baselineSlope := baselineGain / baselineDeltaResource
		if baselineSlope <= 0 || math.IsNaN(baselineSlope) || math.IsInf(baselineSlope, 0) {
			status = WorkloadCurveEfficiencyBlocked
			reason = "baseline_segment_has_no_positive_gain"
			action = "collect-evidence"
		} else {
			for index := 1; index < len(curve.Points); index++ {
				left := curve.Points[index-1]
				right := curve.Points[index]
				deltaResource := right.ResourceFactor - left.ResourceFactor
				if deltaResource <= 0 {
					return WorkloadCurveEfficiencyReport{}, fmt.Errorf("%w: segment resource factors must increase", ErrInvalidRecommendation)
				}
				gain := right.SafeWorkload - left.SafeWorkload
				slope := gain / deltaResource
				if math.IsNaN(slope) || math.IsInf(slope, 0) {
					return WorkloadCurveEfficiencyReport{}, fmt.Errorf("%w: segment marginal workload gain is not finite", ErrInvalidRecommendation)
				}
				ratio := slope / baselineSlope
				if math.IsNaN(ratio) || math.IsInf(ratio, 0) {
					return WorkloadCurveEfficiencyReport{}, fmt.Errorf("%w: segment efficiency ratio is not finite", ErrInvalidRecommendation)
				}
				if ratio < 0 {
					ratio = 0
				}
				if ratio < lowest {
					lowest = ratio
				}
				segments = append(segments, WorkloadCurveEfficiencySegment{
					LeftPointID: left.PointID, RightPointID: right.PointID,
					ResourceFactorStart: left.ResourceFactor, ResourceFactorEnd: right.ResourceFactor,
					SafeWorkloadStart: left.SafeWorkload, SafeWorkloadEnd: right.SafeWorkload,
					MarginalWorkloadGain: slope, EfficiencyRatio: ratio,
				})
			}
			if status == WorkloadCurveEfficiencyEfficient && lowest+floatTolerance(lowest, policy.MinimumEfficiencyRatio) < policy.MinimumEfficiencyRatio {
				status = WorkloadCurveEfficiencyDiminishingReturns
				reason = "marginal_efficiency_below_policy"
				action = "inspect-bottleneck-before-scaling"
			}
		}
	}

	canonical := struct {
		CurveID string                        `json:"curve_id"`
		Policy  WorkloadCurveEfficiencyPolicy `json:"policy"`
	}{curve.CurveID, policy}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return WorkloadCurveEfficiencyReport{}, fmt.Errorf("%w: efficiency fingerprint: %v", ErrInvalidRecommendation, err)
	}
	digest := sha256.Sum256(encoded)
	return WorkloadCurveEfficiencyReport{
		SchemaVersion:      WorkloadCurveEfficiencySchemaV1,
		ReportID:           "wce-" + hex.EncodeToString(digest[:])[:24],
		CurveID:            curve.CurveID,
		ScopeID:            curve.ScopeID,
		WorkloadUnit:       curve.WorkloadUnit,
		Policy:             policy,
		Segments:           segments,
		LowestEfficiency:   lowest,
		Status:             status,
		Reason:             reason,
		RecommendedAction:  action,
		Confidence:         curve.Confidence,
		AdvisoryOnly:       true,
		ProductionMutation: false,
	}, nil
}
