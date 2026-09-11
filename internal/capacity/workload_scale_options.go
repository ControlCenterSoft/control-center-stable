package capacity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

const WorkloadScaleOptionsSchemaV1 = "capacity.workload-scale-options/v1"

type WorkloadScaleOptionsStatus string

const (
	WorkloadScaleOptionsRecommendationAvailable WorkloadScaleOptionsStatus = "recommendation-available"
	WorkloadScaleOptionsDiminishingReturns      WorkloadScaleOptionsStatus = "recommendation-with-diminishing-returns"
	WorkloadScaleOptionsInsufficient            WorkloadScaleOptionsStatus = "insufficient-capacity"
	WorkloadScaleOptionsCollectEvidence         WorkloadScaleOptionsStatus = "collect-evidence"
	WorkloadScaleOptionsBlocked                 WorkloadScaleOptionsStatus = "blocked"
)

type WorkloadScaleOptions struct {
	SchemaVersion             string                     `json:"schema_version"`
	OptionsID                 string                     `json:"options_id"`
	CurveID                   string                     `json:"curve_id"`
	EfficiencyReportID        string                     `json:"efficiency_report_id"`
	RequiredWorkload          float64                    `json:"required_workload"`
	MinimumHeadroomPercent    float64                    `json:"minimum_headroom_percent"`
	Options                   []WorkloadScaleScenario    `json:"options"`
	RecommendedScenarioID     string                     `json:"recommended_scenario_id,omitempty"`
	RecommendedResourceFactor *float64                   `json:"recommended_resource_factor,omitempty"`
	Status                    WorkloadScaleOptionsStatus `json:"status"`
	Reason                    string                     `json:"reason"`
	RecommendedAction         string                     `json:"recommended_action"`
	AdvisoryOnly              bool                       `json:"advisory_only"`
	ProductionMutation        bool                       `json:"production_mutation"`
}

// EvaluateWorkloadScaleOptions compares bounded resource-factor alternatives for
// one fixed demand/headroom target. Every option is rebuilt through
// EvaluateWorkloadScaleScenario, so exact curve and efficiency evidence remains
// authoritative. The result is advisory only and never authorizes scaling.
func EvaluateWorkloadScaleOptions(
	curve WorkloadCurve,
	efficiency WorkloadCurveEfficiencyReport,
	requests []WorkloadScaleScenarioRequest,
) (WorkloadScaleOptions, error) {
	if len(requests) == 0 || len(requests) > 64 {
		return WorkloadScaleOptions{}, fmt.Errorf("%w: scale options must contain between 1 and 64 requests", ErrInvalidRecommendation)
	}

	normalized := append([]WorkloadScaleScenarioRequest(nil), requests...)
	sort.Slice(normalized, func(i, j int) bool {
		if !closeFloat(normalized[i].TargetResourceFactor, normalized[j].TargetResourceFactor) {
			return normalized[i].TargetResourceFactor < normalized[j].TargetResourceFactor
		}
		if !closeFloat(normalized[i].RequiredWorkload, normalized[j].RequiredWorkload) {
			return normalized[i].RequiredWorkload < normalized[j].RequiredWorkload
		}
		return normalized[i].MinimumHeadroomPercent < normalized[j].MinimumHeadroomPercent
	})

	requiredWorkload := normalized[0].RequiredWorkload
	minimumHeadroom := normalized[0].MinimumHeadroomPercent
	for index, request := range normalized {
		if index > 0 && closeFloat(normalized[index-1].TargetResourceFactor, request.TargetResourceFactor) {
			return WorkloadScaleOptions{}, fmt.Errorf("%w: duplicate target_resource_factor", ErrInvalidRecommendation)
		}
		if !closeFloat(request.RequiredWorkload, requiredWorkload) || !closeFloat(request.MinimumHeadroomPercent, minimumHeadroom) {
			return WorkloadScaleOptions{}, fmt.Errorf("%w: all scale options must use the same demand and headroom target", ErrInvalidRecommendation)
		}
	}

	options := make([]WorkloadScaleScenario, 0, len(normalized))
	for _, request := range normalized {
		scenario, err := EvaluateWorkloadScaleScenario(curve, efficiency, request)
		if err != nil {
			return WorkloadScaleOptions{}, err
		}
		options = append(options, scenario)
	}

	result := newWorkloadScaleOptions(curve, efficiency, normalized, options)
	bestIndex := -1
	bestRank := 3
	for index, option := range options {
		rank := 3
		switch option.Status {
		case WorkloadScaleScenarioFeasible:
			rank = 0
		case WorkloadScaleScenarioDiminishingReturns:
			rank = 1
		}
		if rank < 3 && (bestIndex == -1 || rank < bestRank ||
			(rank == bestRank && option.Request.TargetResourceFactor < options[bestIndex].Request.TargetResourceFactor)) {
			bestIndex = index
			bestRank = rank
		}
	}
	if bestIndex >= 0 {
		factor := options[bestIndex].Request.TargetResourceFactor
		result.RecommendedScenarioID = options[bestIndex].ScenarioID
		result.RecommendedResourceFactor = &factor
		if options[bestIndex].Status == WorkloadScaleScenarioDiminishingReturns {
			result.Status = WorkloadScaleOptionsDiminishingReturns
			result.Reason = "minimum_resource_factor_meets_target_with_diminishing_returns"
			result.RecommendedAction = "inspect-bottleneck-before-scaling"
		} else {
			result.Status = WorkloadScaleOptionsRecommendationAvailable
			result.Reason = "minimum_resource_factor_meets_target"
			result.RecommendedAction = "none"
		}
		return result, nil
	}

	hasCollectEvidence := false
	hasBlocked := false
	for _, option := range options {
		switch option.Status {
		case WorkloadScaleScenarioCollectEvidence:
			hasCollectEvidence = true
		case WorkloadScaleScenarioBlocked:
			hasBlocked = true
		}
	}
	switch {
	case hasBlocked:
		result.Status = WorkloadScaleOptionsBlocked
		result.Reason = "one_or_more_scale_options_blocked_by_evidence"
		result.RecommendedAction = "collect-evidence"
	case hasCollectEvidence:
		result.Status = WorkloadScaleOptionsCollectEvidence
		result.Reason = "measured_curve_does_not_cover_a_safe_option"
		result.RecommendedAction = "collect-evidence"
	default:
		result.Status = WorkloadScaleOptionsInsufficient
		result.Reason = "no_evidence_backed_option_meets_required_headroom"
		result.RecommendedAction = "increase-evidence-backed-capacity-or-reduce-demand"
	}
	return result, nil
}

func newWorkloadScaleOptions(
	curve WorkloadCurve,
	efficiency WorkloadCurveEfficiencyReport,
	requests []WorkloadScaleScenarioRequest,
	options []WorkloadScaleScenario,
) WorkloadScaleOptions {
	canonical := struct {
		CurveID            string                         `json:"curve_id"`
		EfficiencyReportID string                         `json:"efficiency_report_id"`
		Requests           []WorkloadScaleScenarioRequest `json:"requests"`
	}{curve.CurveID, efficiency.ReportID, requests}
	encoded, _ := json.Marshal(canonical)
	digest := sha256.Sum256(encoded)
	return WorkloadScaleOptions{
		SchemaVersion:          WorkloadScaleOptionsSchemaV1,
		OptionsID:              "wso-" + hex.EncodeToString(digest[:])[:24],
		CurveID:                curve.CurveID,
		EfficiencyReportID:     efficiency.ReportID,
		RequiredWorkload:       requests[0].RequiredWorkload,
		MinimumHeadroomPercent: requests[0].MinimumHeadroomPercent,
		Options:                options,
		AdvisoryOnly:           true,
		ProductionMutation:     false,
	}
}
