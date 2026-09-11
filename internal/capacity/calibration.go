package capacity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

const CalibrationSchemaV1 = "capacity.calibration/v1"

type CalibrationQuality string

const (
	CalibrationQualityLow    CalibrationQuality = "low"
	CalibrationQualityMedium CalibrationQuality = "medium"
	CalibrationQualityHigh   CalibrationQuality = "high"
)

type CalibrationStatus string

const (
	CalibrationReady           CalibrationStatus = "ready"
	CalibrationCollectEvidence CalibrationStatus = "collect-evidence"
	CalibrationBlocked         CalibrationStatus = "blocked"
)

type CalibrationObservation struct {
	ObservationID     string  `json:"observation_id"`
	PredictedWorkload float64 `json:"predicted_workload"`
	ObservedWorkload  float64 `json:"observed_workload"`
}

type CalibrationRequest struct {
	ScopeID              string       `json:"scope_id"`
	WorkloadUnit         WorkloadUnit `json:"workload_unit"`
	MinimumSamples       int          `json:"minimum_samples"`
	MaxAdjustmentPercent float64      `json:"max_adjustment_percent"`
	MaxP90ErrorPercent   float64      `json:"max_p90_error_percent"`
}

type CalibrationResult struct {
	SchemaVersion            string             `json:"schema_version"`
	CalibrationID            string             `json:"calibration_id"`
	ScopeID                  string             `json:"scope_id"`
	WorkloadUnit             WorkloadUnit       `json:"workload_unit"`
	SampleCount              int                `json:"sample_count"`
	ObservedMultiplier       float64            `json:"observed_multiplier"`
	SuggestedMultiplier      float64            `json:"suggested_multiplier"`
	MeanAbsoluteErrorPercent float64            `json:"mean_absolute_error_percent"`
	P90AbsoluteErrorPercent  float64            `json:"p90_absolute_error_percent"`
	Quality                  CalibrationQuality `json:"quality"`
	Status                   CalibrationStatus  `json:"status"`
	AdjustmentAllowed        bool               `json:"adjustment_allowed"`
	AdvisoryOnly             bool               `json:"advisory_only"`
	ProductionMutation       bool               `json:"production_mutation"`
}

// BuildCalibration compares predicted workload with observed load-test or
// production telemetry. It produces bounded advisory evidence only; it never
// rewrites a CapacityProfile or changes infrastructure.
func BuildCalibration(request CalibrationRequest, observations []CalibrationObservation) (CalibrationResult, error) {
	request.ScopeID = strings.TrimSpace(request.ScopeID)
	if !identifierPattern.MatchString(request.ScopeID) || !validWorkloadUnit(request.WorkloadUnit) {
		return CalibrationResult{}, fmt.Errorf("%w: invalid calibration identity", ErrInvalidRecommendation)
	}
	if request.MinimumSamples < 3 || request.MinimumSamples > 4096 {
		return CalibrationResult{}, fmt.Errorf("%w: minimum_samples must be within [3,4096]", ErrInvalidRecommendation)
	}
	if !finitePositive(request.MaxAdjustmentPercent) || request.MaxAdjustmentPercent > 50 {
		return CalibrationResult{}, fmt.Errorf("%w: max_adjustment_percent must be within (0,50]", ErrInvalidRecommendation)
	}
	if !finitePositive(request.MaxP90ErrorPercent) || request.MaxP90ErrorPercent > 500 {
		return CalibrationResult{}, fmt.Errorf("%w: max_p90_error_percent must be within (0,500]", ErrInvalidRecommendation)
	}
	if len(observations) < request.MinimumSamples || len(observations) > 4096 {
		return CalibrationResult{}, fmt.Errorf("%w: observations do not satisfy minimum_samples or maximum size", ErrInvalidRecommendation)
	}

	normalized := make([]CalibrationObservation, len(observations))
	seen := make(map[string]struct{}, len(observations))
	for i, observation := range observations {
		observation.ObservationID = strings.TrimSpace(observation.ObservationID)
		if !identifierPattern.MatchString(observation.ObservationID) {
			return CalibrationResult{}, fmt.Errorf("%w: invalid observation_id %q", ErrInvalidRecommendation, observation.ObservationID)
		}
		if _, duplicate := seen[observation.ObservationID]; duplicate {
			return CalibrationResult{}, fmt.Errorf("%w: duplicate observation_id %q", ErrInvalidRecommendation, observation.ObservationID)
		}
		if !finitePositive(observation.PredictedWorkload) || !finite(observation.ObservedWorkload) || observation.ObservedWorkload < 0 {
			return CalibrationResult{}, fmt.Errorf("%w: invalid calibration observation %q", ErrInvalidRecommendation, observation.ObservationID)
		}
		ratio := observation.ObservedWorkload / observation.PredictedWorkload
		if !finite(ratio) || ratio > 10 {
			return CalibrationResult{}, fmt.Errorf("%w: observation ratio is outside supported bounds", ErrInvalidRecommendation)
		}
		seen[observation.ObservationID] = struct{}{}
		normalized[i] = observation
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].ObservationID < normalized[j].ObservationID })

	errors := make([]float64, len(normalized))
	meanRatio, meanError := 0.0, 0.0
	for i, observation := range normalized {
		ratio := observation.ObservedWorkload / observation.PredictedWorkload
		errorPercent := math.Abs(observation.ObservedWorkload-observation.PredictedWorkload) / observation.PredictedWorkload * 100
		errors[i] = errorPercent
		meanRatio += ratio
		meanError += errorPercent
	}
	meanRatio /= float64(len(normalized))
	meanError /= float64(len(normalized))
	if !finite(meanRatio) || !finite(meanError) {
		return CalibrationResult{}, fmt.Errorf("%w: calibration aggregate overflow", ErrInvalidRecommendation)
	}
	sort.Float64s(errors)
	p90Index := int(math.Ceil(float64(len(errors))*0.90)) - 1
	if p90Index < 0 {
		p90Index = 0
	}
	p90Error := errors[p90Index]

	quality := CalibrationQualityLow
	if len(normalized) >= 20 && p90Error <= 10 {
		quality = CalibrationQualityHigh
	} else if len(normalized) >= 8 && p90Error <= 25 {
		quality = CalibrationQualityMedium
	}

	status := CalibrationReady
	adjustmentAllowed := true
	suggestedMultiplier := meanRatio
	minimumMultiplier := 1 - request.MaxAdjustmentPercent/100
	maximumMultiplier := 1 + request.MaxAdjustmentPercent/100
	if meanRatio < minimumMultiplier-floatTolerance(meanRatio, minimumMultiplier) || meanRatio > maximumMultiplier+floatTolerance(meanRatio, maximumMultiplier) {
		status = CalibrationBlocked
		adjustmentAllowed = false
		suggestedMultiplier = 1
	} else if p90Error > request.MaxP90ErrorPercent+floatTolerance(p90Error, request.MaxP90ErrorPercent) {
		status = CalibrationCollectEvidence
		adjustmentAllowed = false
		suggestedMultiplier = 1
	}

	canonical := struct {
		Request      CalibrationRequest       `json:"request"`
		Observations []CalibrationObservation `json:"observations"`
	}{request, normalized}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return CalibrationResult{}, fmt.Errorf("%w: calibration fingerprint: %v", ErrInvalidRecommendation, err)
	}
	digest := sha256.Sum256(encoded)
	return CalibrationResult{
		SchemaVersion:            CalibrationSchemaV1,
		CalibrationID:            "cal-" + hex.EncodeToString(digest[:])[:24],
		ScopeID:                  request.ScopeID,
		WorkloadUnit:             request.WorkloadUnit,
		SampleCount:              len(normalized),
		ObservedMultiplier:       meanRatio,
		SuggestedMultiplier:      suggestedMultiplier,
		MeanAbsoluteErrorPercent: meanError,
		P90AbsoluteErrorPercent:  p90Error,
		Quality:                  quality,
		Status:                   status,
		AdjustmentAllowed:        adjustmentAllowed,
		AdvisoryOnly:             true,
		ProductionMutation:       false,
	}, nil
}
