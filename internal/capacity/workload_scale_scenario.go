package capacity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
)

const WorkloadScaleScenarioSchemaV1 = "capacity.workload-scale-scenario/v1"

type WorkloadScaleScenarioStatus string

const (
	WorkloadScaleScenarioFeasible           WorkloadScaleScenarioStatus = "feasible"
	WorkloadScaleScenarioInsufficient       WorkloadScaleScenarioStatus = "insufficient-capacity"
	WorkloadScaleScenarioDiminishingReturns WorkloadScaleScenarioStatus = "diminishing-returns"
	WorkloadScaleScenarioCollectEvidence    WorkloadScaleScenarioStatus = "collect-evidence"
	WorkloadScaleScenarioBlocked            WorkloadScaleScenarioStatus = "blocked"
)

type WorkloadScaleScenarioRequest struct {
	TargetResourceFactor   float64 `json:"target_resource_factor"`
	RequiredWorkload       float64 `json:"required_workload"`
	MinimumHeadroomPercent float64 `json:"minimum_headroom_percent"`
}

type WorkloadScaleScenario struct {
	SchemaVersion         string                       `json:"schema_version"`
	ScenarioID            string                       `json:"scenario_id"`
	CurveID               string                       `json:"curve_id"`
	EfficiencyReportID    string                       `json:"efficiency_report_id"`
	Request               WorkloadScaleScenarioRequest `json:"request"`
	EstimatedSafeWorkload *float64                     `json:"estimated_safe_workload,omitempty"`
	HeadroomWorkload      *float64                     `json:"headroom_workload,omitempty"`
	HeadroomPercent       *float64                     `json:"headroom_percent,omitempty"`
	Status                WorkloadScaleScenarioStatus  `json:"status"`
	Reason                string                       `json:"reason"`
	RecommendedAction     string                       `json:"recommended_action"`
	AdvisoryOnly          bool                         `json:"advisory_only"`
	ProductionMutation    bool                         `json:"production_mutation"`
}

// EvaluateWorkloadScaleScenario evaluates demand only inside a measured workload
// curve and against exact efficiency evidence. It never authorizes resizing.
func EvaluateWorkloadScaleScenario(
	curve WorkloadCurve,
	efficiency WorkloadCurveEfficiencyReport,
	request WorkloadScaleScenarioRequest,
) (WorkloadScaleScenario, error) {
	if math.IsNaN(request.TargetResourceFactor) || math.IsInf(request.TargetResourceFactor, 0) ||
		request.TargetResourceFactor <= 0 || request.TargetResourceFactor > 1000 {
		return WorkloadScaleScenario{}, fmt.Errorf("%w: invalid target_resource_factor", ErrInvalidRecommendation)
	}
	if math.IsNaN(request.RequiredWorkload) || math.IsInf(request.RequiredWorkload, 0) || request.RequiredWorkload <= 0 || request.RequiredWorkload > 1_000_000_000_000 {
		return WorkloadScaleScenario{}, fmt.Errorf("%w: invalid required_workload", ErrInvalidRecommendation)
	}
	if math.IsNaN(request.MinimumHeadroomPercent) || math.IsInf(request.MinimumHeadroomPercent, 0) ||
		request.MinimumHeadroomPercent < 0 || request.MinimumHeadroomPercent > 1000 {
		return WorkloadScaleScenario{}, fmt.Errorf("%w: invalid minimum_headroom_percent", ErrInvalidRecommendation)
	}
	if efficiency.SchemaVersion != WorkloadCurveEfficiencySchemaV1 || !efficiency.AdvisoryOnly || efficiency.ProductionMutation {
		return WorkloadScaleScenario{}, fmt.Errorf("%w: invalid efficiency evidence", ErrInvalidRecommendation)
	}
	rebuiltEfficiency, err := AnalyzeWorkloadCurveEfficiency(curve, efficiency.Policy)
	if err != nil {
		return WorkloadScaleScenario{}, fmt.Errorf("%w: rebuild efficiency evidence: %v", ErrInvalidRecommendation, err)
	}
	if !reflect.DeepEqual(rebuiltEfficiency, efficiency) {
		return WorkloadScaleScenario{}, fmt.Errorf("%w: efficiency evidence mismatch", ErrInvalidRecommendation)
	}

	scenario := newWorkloadScaleScenario(curve, efficiency, request)
	switch efficiency.Status {
	case WorkloadCurveEfficiencyBlocked:
		scenario.Status = WorkloadScaleScenarioBlocked
		scenario.Reason = "efficiency_evidence_blocked"
		scenario.RecommendedAction = "collect-evidence"
		return scenario, nil
	case WorkloadCurveEfficiencyCollectEvidence:
		scenario.Status = WorkloadScaleScenarioCollectEvidence
		scenario.Reason = "efficiency_evidence_incomplete"
		scenario.RecommendedAction = "collect-evidence"
		return scenario, nil
	}

	estimate, err := EstimateWorkloadCurve(curve, request.TargetResourceFactor)
	if err != nil {
		return WorkloadScaleScenario{}, err
	}
	if estimate.Status == WorkloadCurveCollectEvidence {
		scenario.Status = WorkloadScaleScenarioCollectEvidence
		scenario.Reason = estimate.Reason
		scenario.RecommendedAction = "collect-evidence"
		return scenario, nil
	}
	if estimate.Status == WorkloadCurveBlocked || estimate.EstimatedSafeWorkload == nil {
		scenario.Status = WorkloadScaleScenarioBlocked
		scenario.Reason = "curve_estimate_blocked"
		scenario.RecommendedAction = "collect-evidence"
		return scenario, nil
	}

	estimated := *estimate.EstimatedSafeWorkload
	headroom := estimated - request.RequiredWorkload
	headroomPercent := headroom / request.RequiredWorkload * 100
	scenario.EstimatedSafeWorkload = &estimated
	scenario.HeadroomWorkload = &headroom
	scenario.HeadroomPercent = &headroomPercent
	if headroom < 0 || headroomPercent+floatTolerance(headroomPercent, request.MinimumHeadroomPercent) < request.MinimumHeadroomPercent {
		scenario.Status = WorkloadScaleScenarioInsufficient
		scenario.Reason = "required_headroom_not_met"
		scenario.RecommendedAction = "increase-evidence-backed-capacity-or-reduce-demand"
		return scenario, nil
	}
	if efficiency.Status == WorkloadCurveEfficiencyDiminishingReturns {
		scenario.Status = WorkloadScaleScenarioDiminishingReturns
		scenario.Reason = "capacity_fits_but_curve_has_diminishing_returns"
		scenario.RecommendedAction = "inspect-bottleneck-before-scaling"
		return scenario, nil
	}
	scenario.Status = WorkloadScaleScenarioFeasible
	scenario.Reason = "required_headroom_met_within_observed_curve"
	scenario.RecommendedAction = "none"
	return scenario, nil
}

func newWorkloadScaleScenario(curve WorkloadCurve, efficiency WorkloadCurveEfficiencyReport, request WorkloadScaleScenarioRequest) WorkloadScaleScenario {
	canonical := struct {
		CurveID            string                       `json:"curve_id"`
		EfficiencyReportID string                       `json:"efficiency_report_id"`
		Request            WorkloadScaleScenarioRequest `json:"request"`
	}{curve.CurveID, efficiency.ReportID, request}
	encoded, _ := json.Marshal(canonical)
	digest := sha256.Sum256(encoded)
	return WorkloadScaleScenario{
		SchemaVersion:      WorkloadScaleScenarioSchemaV1,
		ScenarioID:         "wss-" + hex.EncodeToString(digest[:])[:24],
		CurveID:            curve.CurveID,
		EfficiencyReportID: efficiency.ReportID,
		Request:            request,
		AdvisoryOnly:       true,
		ProductionMutation: false,
	}
}
