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

const ForecastSchemaV1 = "capacity.forecast/v1"

type ForecastQuality string

const (
	ForecastQualityLow    ForecastQuality = "low"
	ForecastQualityMedium ForecastQuality = "medium"
	ForecastQualityHigh   ForecastQuality = "high"
)

// GrowthObservation is a normalized workload sample on a monotonically
// increasing time axis expressed in days. The caller may derive Day from real
// telemetry timestamps before entering the planner boundary.
type GrowthObservation struct {
	ObservationID string  `json:"observation_id"`
	Day           float64 `json:"day"`
	Workload      float64 `json:"workload"`
}

type ForecastRequest struct {
	ScopeID             string       `json:"scope_id"`
	WorkloadUnit        WorkloadUnit `json:"workload_unit"`
	HorizonDays         float64      `json:"horizon_days"`
	SafetyMarginPercent float64      `json:"safety_margin_percent"`
}

// Forecast is advisory planning evidence. PlanningWorkload includes the safety
// margin and can be fed into BuildAssessment as ExpectedWorkload.
type Forecast struct {
	SchemaVersion       string          `json:"schema_version"`
	ForecastID          string          `json:"forecast_id"`
	ScopeID             string          `json:"scope_id"`
	WorkloadUnit        WorkloadUnit    `json:"workload_unit"`
	SampleCount         int             `json:"sample_count"`
	CurrentWorkload     float64         `json:"current_workload"`
	DailyGrowth         float64         `json:"daily_growth"`
	HorizonDays         float64         `json:"horizon_days"`
	ProjectedWorkload   float64         `json:"projected_workload"`
	SafetyMarginPercent float64         `json:"safety_margin_percent"`
	PlanningWorkload    float64         `json:"planning_workload"`
	FitRSquared         float64         `json:"fit_r_squared"`
	Quality             ForecastQuality `json:"quality"`
	AdvisoryOnly        bool            `json:"advisory_only"`
	ProductionMutation  bool            `json:"production_mutation"`
}

// BuildForecast fits a deterministic least-squares trend over bounded telemetry
// observations and projects workload at the requested horizon. It never
// schedules work or mutates infrastructure.
func BuildForecast(request ForecastRequest, observations []GrowthObservation) (Forecast, error) {
	request.ScopeID = strings.TrimSpace(request.ScopeID)
	if !identifierPattern.MatchString(request.ScopeID) || !validWorkloadUnit(request.WorkloadUnit) {
		return Forecast{}, fmt.Errorf("%w: invalid forecast identity", ErrInvalidRecommendation)
	}
	if !finitePositive(request.HorizonDays) || request.HorizonDays > 3650 {
		return Forecast{}, fmt.Errorf("%w: horizon_days must be within (0,3650]", ErrInvalidRecommendation)
	}
	if !finite(request.SafetyMarginPercent) || request.SafetyMarginPercent < 0 || request.SafetyMarginPercent > 200 {
		return Forecast{}, fmt.Errorf("%w: safety_margin_percent must be within [0,200]", ErrInvalidRecommendation)
	}
	if len(observations) < 2 || len(observations) > 4096 {
		return Forecast{}, fmt.Errorf("%w: observations must contain between 2 and 4096 items", ErrInvalidRecommendation)
	}

	normalized := make([]GrowthObservation, len(observations))
	seenID := make(map[string]struct{}, len(observations))
	for i, observation := range observations {
		observation.ObservationID = strings.TrimSpace(observation.ObservationID)
		if !identifierPattern.MatchString(observation.ObservationID) {
			return Forecast{}, fmt.Errorf("%w: invalid observation_id %q", ErrInvalidRecommendation, observation.ObservationID)
		}
		if _, duplicate := seenID[observation.ObservationID]; duplicate {
			return Forecast{}, fmt.Errorf("%w: duplicate observation_id %q", ErrInvalidRecommendation, observation.ObservationID)
		}
		if !finite(observation.Day) || observation.Day < 0 || !finite(observation.Workload) || observation.Workload < 0 {
			return Forecast{}, fmt.Errorf("%w: invalid observation %q", ErrInvalidRecommendation, observation.ObservationID)
		}
		seenID[observation.ObservationID] = struct{}{}
		normalized[i] = observation
	}
	sort.Slice(normalized, func(i, j int) bool {
		if !closeFloat(normalized[i].Day, normalized[j].Day) {
			return normalized[i].Day < normalized[j].Day
		}
		return normalized[i].ObservationID < normalized[j].ObservationID
	})
	for i := 1; i < len(normalized); i++ {
		if closeFloat(normalized[i-1].Day, normalized[i].Day) {
			return Forecast{}, fmt.Errorf("%w: observation days must be unique", ErrInvalidRecommendation)
		}
	}

	meanDay, meanWorkload := 0.0, 0.0
	for _, observation := range normalized {
		meanDay += observation.Day
		meanWorkload += observation.Workload
	}
	meanDay /= float64(len(normalized))
	meanWorkload /= float64(len(normalized))

	covariance, dayVariance := 0.0, 0.0
	for _, observation := range normalized {
		dx := observation.Day - meanDay
		covariance += dx * (observation.Workload - meanWorkload)
		dayVariance += dx * dx
	}
	if dayVariance <= floatTolerance(dayVariance) {
		return Forecast{}, fmt.Errorf("%w: observation time span is too small", ErrInvalidRecommendation)
	}
	slope := covariance / dayVariance
	intercept := meanWorkload - slope*meanDay

	residualSquares, totalSquares := 0.0, 0.0
	for _, observation := range normalized {
		predicted := intercept + slope*observation.Day
		residual := observation.Workload - predicted
		centered := observation.Workload - meanWorkload
		residualSquares += residual * residual
		totalSquares += centered * centered
	}
	rSquared := 1.0
	if totalSquares > floatTolerance(totalSquares) {
		rSquared = 1 - residualSquares/totalSquares
		if rSquared < 0 {
			rSquared = 0
		} else if rSquared > 1 {
			rSquared = 1
		}
	}

	current := normalized[len(normalized)-1].Workload
	projected := current + slope*request.HorizonDays
	if projected < 0 {
		projected = 0
	}
	planning := projected * (1 + request.SafetyMarginPercent/100)
	if !finite(planning) || math.IsInf(planning, 0) {
		return Forecast{}, fmt.Errorf("%w: projected workload overflow", ErrInvalidRecommendation)
	}

	quality := ForecastQualityLow
	if len(normalized) >= 8 && rSquared >= .8 {
		quality = ForecastQualityHigh
	} else if len(normalized) >= 4 && rSquared >= .5 {
		quality = ForecastQualityMedium
	}

	canonical := struct {
		Request      ForecastRequest     `json:"request"`
		Observations []GrowthObservation `json:"observations"`
	}{request, normalized}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return Forecast{}, fmt.Errorf("%w: forecast fingerprint: %v", ErrInvalidRecommendation, err)
	}
	digest := sha256.Sum256(encoded)
	return Forecast{
		SchemaVersion:       ForecastSchemaV1,
		ForecastID:          "fc-" + hex.EncodeToString(digest[:])[:24],
		ScopeID:             request.ScopeID,
		WorkloadUnit:        request.WorkloadUnit,
		SampleCount:         len(normalized),
		CurrentWorkload:     current,
		DailyGrowth:         slope,
		HorizonDays:         request.HorizonDays,
		ProjectedWorkload:   projected,
		SafetyMarginPercent: request.SafetyMarginPercent,
		PlanningWorkload:    planning,
		FitRSquared:         rSquared,
		Quality:             quality,
		AdvisoryOnly:        true,
		ProductionMutation:  false,
	}, nil
}
