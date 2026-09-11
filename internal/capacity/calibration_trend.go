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

const CalibrationTrendSchemaV1 = "capacity.calibration-trend/v1"

type CalibrationTrendStatus string

const (
	CalibrationTrendStable          CalibrationTrendStatus = "stable"
	CalibrationTrendDrifting        CalibrationTrendStatus = "drifting"
	CalibrationTrendCollectEvidence CalibrationTrendStatus = "collect-evidence"
	CalibrationTrendBlocked         CalibrationTrendStatus = "blocked"
)

type CalibrationSnapshot struct {
	SnapshotID  string            `json:"snapshot_id"`
	Day         float64           `json:"day"`
	Calibration CalibrationResult `json:"calibration"`
}

type CalibrationTrendRequest struct {
	ScopeID                        string             `json:"scope_id"`
	WorkloadUnit                   WorkloadUnit       `json:"workload_unit"`
	MinimumSnapshots               int                `json:"minimum_snapshots"`
	MinimumQuality                 CalibrationQuality `json:"minimum_quality"`
	MaxMultiplierDriftPer30DaysPct float64            `json:"max_multiplier_drift_per_30_days_percent"`
}

type CalibrationTrend struct {
	SchemaVersion                   string                 `json:"schema_version"`
	TrendID                         string                 `json:"trend_id"`
	ScopeID                         string                 `json:"scope_id"`
	WorkloadUnit                    WorkloadUnit           `json:"workload_unit"`
	SnapshotCount                   int                    `json:"snapshot_count"`
	SpanDays                        float64                `json:"span_days"`
	FirstMultiplier                 float64                `json:"first_multiplier"`
	LatestMultiplier                float64                `json:"latest_multiplier"`
	MultiplierDriftPer30DaysPercent float64                `json:"multiplier_drift_per_30_days_percent"`
	LatestP90ErrorPercent           float64                `json:"latest_p90_error_percent"`
	Status                          CalibrationTrendStatus `json:"status"`
	Reason                          string                 `json:"reason"`
	AdvisoryOnly                    bool                   `json:"advisory_only"`
	ProductionMutation              bool                   `json:"production_mutation"`
}

// BuildCalibrationTrend detects model-calibration drift across ordered evidence
// snapshots. It never modifies a profile or forecast and produces advisory
// evidence only.
func BuildCalibrationTrend(request CalibrationTrendRequest, snapshots []CalibrationSnapshot) (CalibrationTrend, error) {
	request.ScopeID = strings.TrimSpace(request.ScopeID)
	if !identifierPattern.MatchString(request.ScopeID) || !validWorkloadUnit(request.WorkloadUnit) {
		return CalibrationTrend{}, fmt.Errorf("%w: invalid calibration trend identity", ErrInvalidRecommendation)
	}
	if request.MinimumSnapshots < 3 || request.MinimumSnapshots > 512 || len(snapshots) < request.MinimumSnapshots || len(snapshots) > 512 {
		return CalibrationTrend{}, fmt.Errorf("%w: invalid calibration trend sample count", ErrInvalidRecommendation)
	}
	if !validCalibrationQuality(request.MinimumQuality) {
		return CalibrationTrend{}, fmt.Errorf("%w: invalid minimum calibration quality", ErrInvalidRecommendation)
	}
	if !finitePositive(request.MaxMultiplierDriftPer30DaysPct) || request.MaxMultiplierDriftPer30DaysPct > 100 {
		return CalibrationTrend{}, fmt.Errorf("%w: invalid drift policy", ErrInvalidRecommendation)
	}

	normalized := make([]CalibrationSnapshot, len(snapshots))
	seen := make(map[string]struct{}, len(snapshots))
	for i, snapshot := range snapshots {
		snapshot.SnapshotID = strings.TrimSpace(snapshot.SnapshotID)
		if !identifierPattern.MatchString(snapshot.SnapshotID) || !finite(snapshot.Day) || snapshot.Day < 0 {
			return CalibrationTrend{}, fmt.Errorf("%w: invalid calibration snapshot", ErrInvalidRecommendation)
		}
		if _, duplicate := seen[snapshot.SnapshotID]; duplicate {
			return CalibrationTrend{}, fmt.Errorf("%w: duplicate calibration snapshot", ErrInvalidRecommendation)
		}
		calibration := snapshot.Calibration
		if calibration.SchemaVersion != CalibrationSchemaV1 || calibration.ScopeID != request.ScopeID || calibration.WorkloadUnit != request.WorkloadUnit {
			return CalibrationTrend{}, fmt.Errorf("%w: calibration snapshot binding mismatch", ErrInvalidRecommendation)
		}
		if !calibration.AdvisoryOnly || calibration.ProductionMutation || !validCalibrationStatus(calibration.Status) || !validCalibrationQuality(calibration.Quality) {
			return CalibrationTrend{}, fmt.Errorf("%w: invalid calibration snapshot evidence", ErrInvalidRecommendation)
		}
		if calibration.Status == CalibrationReady && !calibration.AdjustmentAllowed {
			return CalibrationTrend{}, fmt.Errorf("%w: ready calibration snapshot must allow adjustment", ErrInvalidRecommendation)
		}
		if calibration.Status != CalibrationReady && (calibration.AdjustmentAllowed || !closeFloat(calibration.SuggestedMultiplier, 1)) {
			return CalibrationTrend{}, fmt.Errorf("%w: non-ready calibration snapshot cannot carry an adjustment", ErrInvalidRecommendation)
		}
		if !finite(calibration.SuggestedMultiplier) || calibration.SuggestedMultiplier < 0.5 || calibration.SuggestedMultiplier > 1.5 ||
			!finite(calibration.P90AbsoluteErrorPercent) || calibration.P90AbsoluteErrorPercent < 0 {
			return CalibrationTrend{}, fmt.Errorf("%w: invalid calibration snapshot values", ErrInvalidRecommendation)
		}
		seen[snapshot.SnapshotID] = struct{}{}
		normalized[i] = snapshot
	}
	sort.Slice(normalized, func(i, j int) bool {
		if !closeFloat(normalized[i].Day, normalized[j].Day) {
			return normalized[i].Day < normalized[j].Day
		}
		return normalized[i].SnapshotID < normalized[j].SnapshotID
	})
	for i := 1; i < len(normalized); i++ {
		if closeFloat(normalized[i-1].Day, normalized[i].Day) {
			return CalibrationTrend{}, fmt.Errorf("%w: calibration snapshot days must be unique", ErrInvalidRecommendation)
		}
	}

	first := normalized[0]
	latest := normalized[len(normalized)-1]
	span := latest.Day - first.Day
	if span <= floatTolerance(span) {
		return CalibrationTrend{}, fmt.Errorf("%w: calibration trend span is too small", ErrInvalidRecommendation)
	}
	firstMultiplier := first.Calibration.SuggestedMultiplier
	latestMultiplier := latest.Calibration.SuggestedMultiplier
	drift := (latestMultiplier - firstMultiplier) / firstMultiplier * 100 * 30 / span
	if !finite(drift) {
		return CalibrationTrend{}, fmt.Errorf("%w: calibration trend overflow", ErrInvalidRecommendation)
	}

	status := CalibrationTrendStable
	reason := "within-drift-policy"
	for _, snapshot := range normalized {
		calibration := snapshot.Calibration
		if calibration.Status == CalibrationBlocked {
			status = CalibrationTrendBlocked
			reason = "calibration-blocked"
			break
		}
		if calibration.Status == CalibrationCollectEvidence || calibrationQualityRank(calibration.Quality) < calibrationQualityRank(request.MinimumQuality) {
			status = CalibrationTrendCollectEvidence
			reason = "insufficient-calibration-evidence"
		}
	}
	if status == CalibrationTrendStable && math.Abs(drift) > request.MaxMultiplierDriftPer30DaysPct+floatTolerance(drift, request.MaxMultiplierDriftPer30DaysPct) {
		status = CalibrationTrendDrifting
		reason = "multiplier-drift-exceeds-policy"
	}

	canonical := struct {
		Request   CalibrationTrendRequest `json:"request"`
		Snapshots []CalibrationSnapshot   `json:"snapshots"`
	}{request, normalized}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return CalibrationTrend{}, fmt.Errorf("%w: calibration trend fingerprint: %v", ErrInvalidRecommendation, err)
	}
	digest := sha256.Sum256(encoded)
	return CalibrationTrend{
		SchemaVersion:                   CalibrationTrendSchemaV1,
		TrendID:                         "cat-" + hex.EncodeToString(digest[:])[:24],
		ScopeID:                         request.ScopeID,
		WorkloadUnit:                    request.WorkloadUnit,
		SnapshotCount:                   len(normalized),
		SpanDays:                        span,
		FirstMultiplier:                 firstMultiplier,
		LatestMultiplier:                latestMultiplier,
		MultiplierDriftPer30DaysPercent: drift,
		LatestP90ErrorPercent:           latest.Calibration.P90AbsoluteErrorPercent,
		Status:                          status,
		Reason:                          reason,
		AdvisoryOnly:                    true,
		ProductionMutation:              false,
	}, nil
}
