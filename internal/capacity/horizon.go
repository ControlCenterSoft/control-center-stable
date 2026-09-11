package capacity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

const CapacityHorizonSchemaV1 = "capacity.horizon/v1"

type CapacityHorizonRisk string

const (
	HorizonHealthy  CapacityHorizonRisk = "healthy"
	HorizonUnknown  CapacityHorizonRisk = "unknown"
	HorizonWarning  CapacityHorizonRisk = "warning"
	HorizonCritical CapacityHorizonRisk = "critical"
)

type CapacityHorizonRequest struct {
	ScopeID             string       `json:"scope_id"`
	WorkloadUnit        WorkloadUnit `json:"workload_unit"`
	WarningHorizonDays  float64      `json:"warning_horizon_days"`
	CriticalHorizonDays float64      `json:"critical_horizon_days"`
}

type CapacityHorizon struct {
	SchemaVersion       string               `json:"schema_version"`
	HorizonID           string               `json:"horizon_id"`
	ScopeID             string               `json:"scope_id"`
	WorkloadUnit        WorkloadUnit         `json:"workload_unit"`
	ForecastID          string               `json:"forecast_id"`
	AssessmentID        string               `json:"assessment_id"`
	CurrentWorkload     float64              `json:"current_workload"`
	DailyGrowth         float64              `json:"daily_growth"`
	SafeCapacity        float64              `json:"safe_capacity"`
	CurrentSafeReserve  float64              `json:"current_safe_reserve"`
	DaysToSafeCapacity  *float64             `json:"days_to_safe_capacity"`
	WarningHorizonDays  float64              `json:"warning_horizon_days"`
	CriticalHorizonDays float64              `json:"critical_horizon_days"`
	Risk                CapacityHorizonRisk  `json:"risk"`
	Action              RecommendationAction `json:"action"`
	AdvisoryOnly        bool                 `json:"advisory_only"`
	ProductionMutation  bool                 `json:"production_mutation"`
}

// BuildCapacityHorizon converts an evidence-backed workload trend and the
// failure-reserved fleet assessment into an advisory time-to-capacity signal.
// It does not resize nodes, place workloads, or mutate infrastructure.
func BuildCapacityHorizon(request CapacityHorizonRequest, forecast Forecast, assessment Assessment) (CapacityHorizon, error) {
	request.ScopeID = strings.TrimSpace(request.ScopeID)
	if !identifierPattern.MatchString(request.ScopeID) || !validWorkloadUnit(request.WorkloadUnit) {
		return CapacityHorizon{}, fmt.Errorf("%w: invalid horizon identity", ErrInvalidRecommendation)
	}
	if !finitePositive(request.WarningHorizonDays) || request.WarningHorizonDays > 3650 || !finite(request.CriticalHorizonDays) || request.CriticalHorizonDays < 0 || request.CriticalHorizonDays > request.WarningHorizonDays {
		return CapacityHorizon{}, fmt.Errorf("%w: horizon thresholds must satisfy 0 <= critical <= warning <= 3650", ErrInvalidRecommendation)
	}
	if forecast.SchemaVersion != ForecastSchemaV1 || assessment.SchemaVersion != AssessmentSchemaV1 {
		return CapacityHorizon{}, fmt.Errorf("%w: unsupported planning evidence", ErrInvalidRecommendation)
	}
	if forecast.ScopeID != request.ScopeID || assessment.ScopeID != request.ScopeID || forecast.WorkloadUnit != request.WorkloadUnit || assessment.WorkloadUnit != request.WorkloadUnit {
		return CapacityHorizon{}, fmt.Errorf("%w: planning evidence binding mismatch", ErrInvalidRecommendation)
	}
	if !forecast.AdvisoryOnly || forecast.ProductionMutation || !assessment.AdvisoryOnly || assessment.ProductionMutation {
		return CapacityHorizon{}, fmt.Errorf("%w: unsafe planning evidence", ErrInvalidRecommendation)
	}
	if !identifierPattern.MatchString(forecast.ForecastID) || !identifierPattern.MatchString(assessment.AssessmentID) || !finite(forecast.CurrentWorkload) || forecast.CurrentWorkload < 0 || !finite(forecast.DailyGrowth) || !finitePositive(assessment.SafeCapacity) {
		return CapacityHorizon{}, fmt.Errorf("%w: invalid planning evidence values", ErrInvalidRecommendation)
	}
	if forecast.Quality != ForecastQualityLow && forecast.Quality != ForecastQualityMedium && forecast.Quality != ForecastQualityHigh {
		return CapacityHorizon{}, fmt.Errorf("%w: invalid forecast quality", ErrInvalidRecommendation)
	}
	if assessment.Confidence.Level != ConfidenceLow && assessment.Confidence.Level != ConfidenceMedium && assessment.Confidence.Level != ConfidenceHigh && assessment.Confidence.Level != ConfidenceCertified {
		return CapacityHorizon{}, fmt.Errorf("%w: invalid assessment confidence", ErrInvalidRecommendation)
	}

	reserve := assessment.SafeCapacity - forecast.CurrentWorkload
	risk := HorizonHealthy
	action := ActionNone
	var days *float64

	switch {
	case forecast.Quality == ForecastQualityLow || assessment.Confidence.Level == ConfidenceLow || assessment.Action == ActionCollectEvidence:
		risk = HorizonUnknown
		action = ActionCollectEvidence
	case !assessment.Safe:
		risk = HorizonCritical
		action = ActionAddRoleCapacity
	case reserve <= floatTolerance(reserve):
		zero := 0.0
		days = &zero
		risk = HorizonCritical
		action = ActionAddRoleCapacity
	case forecast.DailyGrowth > floatTolerance(forecast.DailyGrowth):
		value := reserve / forecast.DailyGrowth
		if !finite(value) || math.IsInf(value, 0) || value < 0 {
			return CapacityHorizon{}, fmt.Errorf("%w: invalid capacity horizon", ErrInvalidRecommendation)
		}
		days = &value
		switch {
		case value <= request.CriticalHorizonDays+floatTolerance(value, request.CriticalHorizonDays):
			risk = HorizonCritical
			action = ActionAddRoleCapacity
		case value <= request.WarningHorizonDays+floatTolerance(value, request.WarningHorizonDays):
			risk = HorizonWarning
			action = ActionAdjustWorkloadPolicy
		}
	}

	canonical := struct {
		Request      CapacityHorizonRequest `json:"request"`
		ForecastID   string                 `json:"forecast_id"`
		AssessmentID string                 `json:"assessment_id"`
		Current      float64                `json:"current_workload"`
		Growth       float64                `json:"daily_growth"`
		SafeCapacity float64                `json:"safe_capacity"`
	}{request, forecast.ForecastID, assessment.AssessmentID, forecast.CurrentWorkload, forecast.DailyGrowth, assessment.SafeCapacity}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return CapacityHorizon{}, fmt.Errorf("%w: horizon fingerprint: %v", ErrInvalidRecommendation, err)
	}
	digest := sha256.Sum256(encoded)
	return CapacityHorizon{
		SchemaVersion:       CapacityHorizonSchemaV1,
		HorizonID:           "ch-" + hex.EncodeToString(digest[:])[:24],
		ScopeID:             request.ScopeID,
		WorkloadUnit:        request.WorkloadUnit,
		ForecastID:          forecast.ForecastID,
		AssessmentID:        assessment.AssessmentID,
		CurrentWorkload:     forecast.CurrentWorkload,
		DailyGrowth:         forecast.DailyGrowth,
		SafeCapacity:        assessment.SafeCapacity,
		CurrentSafeReserve:  reserve,
		DaysToSafeCapacity:  days,
		WarningHorizonDays:  request.WarningHorizonDays,
		CriticalHorizonDays: request.CriticalHorizonDays,
		Risk:                risk,
		Action:              action,
		AdvisoryOnly:        true,
		ProductionMutation:  false,
	}, nil
}
