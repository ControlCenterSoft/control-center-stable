package capacity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

const ForecastCorrectionSchemaV1 = "capacity.forecast-correction/v1"

type ForecastCorrectionStatus string

const (
	ForecastCorrectionReady   ForecastCorrectionStatus = "ready"
	ForecastCorrectionBlocked ForecastCorrectionStatus = "blocked"
)

type ForecastCorrectionRequest struct {
	Forecast       Forecast           `json:"forecast"`
	Calibration    CalibrationResult  `json:"calibration"`
	MinimumQuality CalibrationQuality `json:"minimum_quality"`
}

type ForecastCorrection struct {
	SchemaVersion              string                   `json:"schema_version"`
	CorrectionID               string                   `json:"correction_id"`
	ForecastID                 string                   `json:"forecast_id"`
	CalibrationID              string                   `json:"calibration_id"`
	ScopeID                    string                   `json:"scope_id"`
	WorkloadUnit               WorkloadUnit             `json:"workload_unit"`
	OriginalProjectedWorkload  float64                  `json:"original_projected_workload"`
	OriginalPlanningWorkload   float64                  `json:"original_planning_workload"`
	CalibrationMultiplier      float64                  `json:"calibration_multiplier"`
	EffectiveMultiplier        float64                  `json:"effective_multiplier"`
	CorrectedProjectedWorkload float64                  `json:"corrected_projected_workload"`
	CorrectedPlanningWorkload  float64                  `json:"corrected_planning_workload"`
	ConservativeFloorApplied   bool                     `json:"conservative_floor_applied"`
	Status                     ForecastCorrectionStatus `json:"status"`
	Reason                     string                   `json:"reason"`
	AdvisoryOnly               bool                     `json:"advisory_only"`
	ProductionMutation         bool                     `json:"production_mutation"`
}

// BuildForecastCorrection binds one immutable forecast to one calibration
// result. A calibration may increase planning load but is never allowed to
// reduce it below the original forecast: optimistic corrections are clamped to
// a multiplier of 1. The result remains advisory and has no mutation authority.
func BuildForecastCorrection(request ForecastCorrectionRequest) (ForecastCorrection, error) {
	forecast := request.Forecast
	calibration := request.Calibration
	if strings.TrimSpace(forecast.SchemaVersion) != ForecastSchemaV1 ||
		strings.TrimSpace(calibration.SchemaVersion) != CalibrationSchemaV1 {
		return ForecastCorrection{}, fmt.Errorf("%w: unsupported forecast correction input schema", ErrInvalidRecommendation)
	}
	if !identifierPattern.MatchString(forecast.ForecastID) || !identifierPattern.MatchString(calibration.CalibrationID) {
		return ForecastCorrection{}, fmt.Errorf("%w: invalid correction input identity", ErrInvalidRecommendation)
	}
	if forecast.ScopeID != calibration.ScopeID || forecast.WorkloadUnit != calibration.WorkloadUnit {
		return ForecastCorrection{}, fmt.Errorf("%w: forecast and calibration scope mismatch", ErrInvalidRecommendation)
	}
	if !identifierPattern.MatchString(forecast.ScopeID) || !validWorkloadUnit(forecast.WorkloadUnit) {
		return ForecastCorrection{}, fmt.Errorf("%w: invalid correction scope", ErrInvalidRecommendation)
	}
	if !forecast.AdvisoryOnly || forecast.ProductionMutation || !calibration.AdvisoryOnly || calibration.ProductionMutation {
		return ForecastCorrection{}, fmt.Errorf("%w: correction inputs must be advisory-only", ErrInvalidRecommendation)
	}
	if !finite(forecast.ProjectedWorkload) || forecast.ProjectedWorkload < 0 ||
		!finite(forecast.PlanningWorkload) || forecast.PlanningWorkload < forecast.ProjectedWorkload-floatTolerance(forecast.PlanningWorkload, forecast.ProjectedWorkload) {
		return ForecastCorrection{}, fmt.Errorf("%w: invalid forecast workload", ErrInvalidRecommendation)
	}
	if !validCalibrationQuality(request.MinimumQuality) || !validCalibrationQuality(calibration.Quality) {
		return ForecastCorrection{}, fmt.Errorf("%w: invalid calibration quality", ErrInvalidRecommendation)
	}
	if !validCalibrationStatus(calibration.Status) {
		return ForecastCorrection{}, fmt.Errorf("%w: invalid calibration status", ErrInvalidRecommendation)
	}
	if !finite(calibration.SuggestedMultiplier) || calibration.SuggestedMultiplier < 0.5 || calibration.SuggestedMultiplier > 1.5 {
		return ForecastCorrection{}, fmt.Errorf("%w: invalid calibration multiplier", ErrInvalidRecommendation)
	}
	if calibration.Status == CalibrationReady && !calibration.AdjustmentAllowed {
		return ForecastCorrection{}, fmt.Errorf("%w: ready calibration must allow adjustment", ErrInvalidRecommendation)
	}
	if calibration.Status != CalibrationReady && (calibration.AdjustmentAllowed || !closeFloat(calibration.SuggestedMultiplier, 1)) {
		return ForecastCorrection{}, fmt.Errorf("%w: blocked calibration cannot allow adjustment", ErrInvalidRecommendation)
	}

	status := ForecastCorrectionReady
	reason := "calibration-ready"
	calibrationMultiplier := calibration.SuggestedMultiplier
	effectiveMultiplier := math.Max(1, calibrationMultiplier)
	conservativeFloor := calibrationMultiplier < 1-floatTolerance(calibrationMultiplier, 1)
	if calibration.Status != CalibrationReady {
		status = ForecastCorrectionBlocked
		reason = "calibration-not-ready"
		calibrationMultiplier = 1
		effectiveMultiplier = 1
		conservativeFloor = false
	} else if calibrationQualityRank(calibration.Quality) < calibrationQualityRank(request.MinimumQuality) {
		status = ForecastCorrectionBlocked
		reason = "calibration-quality-below-policy"
		calibrationMultiplier = 1
		effectiveMultiplier = 1
		conservativeFloor = false
	} else if conservativeFloor {
		reason = "conservative-floor"
	}

	projected := forecast.ProjectedWorkload * effectiveMultiplier
	planning := forecast.PlanningWorkload * effectiveMultiplier
	if !finite(projected) || !finite(planning) {
		return ForecastCorrection{}, fmt.Errorf("%w: corrected workload overflow", ErrInvalidRecommendation)
	}

	canonical := struct {
		ForecastID     string             `json:"forecast_id"`
		CalibrationID  string             `json:"calibration_id"`
		MinimumQuality CalibrationQuality `json:"minimum_quality"`
	}{forecast.ForecastID, calibration.CalibrationID, request.MinimumQuality}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return ForecastCorrection{}, fmt.Errorf("%w: correction fingerprint: %v", ErrInvalidRecommendation, err)
	}
	digest := sha256.Sum256(encoded)
	return ForecastCorrection{
		SchemaVersion:              ForecastCorrectionSchemaV1,
		CorrectionID:               "fcc-" + hex.EncodeToString(digest[:])[:24],
		ForecastID:                 forecast.ForecastID,
		CalibrationID:              calibration.CalibrationID,
		ScopeID:                    forecast.ScopeID,
		WorkloadUnit:               forecast.WorkloadUnit,
		OriginalProjectedWorkload:  forecast.ProjectedWorkload,
		OriginalPlanningWorkload:   forecast.PlanningWorkload,
		CalibrationMultiplier:      calibrationMultiplier,
		EffectiveMultiplier:        effectiveMultiplier,
		CorrectedProjectedWorkload: projected,
		CorrectedPlanningWorkload:  planning,
		ConservativeFloorApplied:   conservativeFloor,
		Status:                     status,
		Reason:                     reason,
		AdvisoryOnly:               true,
		ProductionMutation:         false,
	}, nil
}

func validCalibrationQuality(value CalibrationQuality) bool {
	return value == CalibrationQualityLow || value == CalibrationQualityMedium || value == CalibrationQualityHigh
}

func validCalibrationStatus(value CalibrationStatus) bool {
	return value == CalibrationReady || value == CalibrationCollectEvidence || value == CalibrationBlocked
}

func calibrationQualityRank(value CalibrationQuality) int {
	switch value {
	case CalibrationQualityHigh:
		return 3
	case CalibrationQualityMedium:
		return 2
	case CalibrationQualityLow:
		return 1
	default:
		return 0
	}
}
