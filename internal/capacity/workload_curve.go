package capacity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	WorkloadCurveSchemaV1         = "capacity.workload-curve/v1"
	WorkloadCurveEstimateSchemaV1 = "capacity.workload-curve-estimate/v1"
)

type WorkloadCurveStatus string

const (
	WorkloadCurveReady           WorkloadCurveStatus = "ready"
	WorkloadCurveCollectEvidence WorkloadCurveStatus = "collect-evidence"
	WorkloadCurveBlocked         WorkloadCurveStatus = "blocked"
)

type WorkloadCurvePoint struct {
	PointID         string                    `json:"point_id"`
	SourceProfileID string                    `json:"source_profile_id"`
	ResourceFactor  float64                   `json:"resource_factor"`
	SafeWorkload    float64                   `json:"safe_workload"`
	Confidence      WorkloadProfileConfidence `json:"confidence"`
}

type WorkloadCurveRequest struct {
	ScopeID       string       `json:"scope_id"`
	WorkloadUnit  WorkloadUnit `json:"workload_unit"`
	MinimumPoints int          `json:"minimum_points"`
}

type WorkloadCurve struct {
	SchemaVersion      string                    `json:"schema_version"`
	CurveID            string                    `json:"curve_id"`
	ScopeID            string                    `json:"scope_id"`
	WorkloadUnit       WorkloadUnit              `json:"workload_unit"`
	MinimumPoints      int                       `json:"minimum_points"`
	Points             []WorkloadCurvePoint      `json:"points"`
	Status             WorkloadCurveStatus       `json:"status"`
	Reason             string                    `json:"reason"`
	Confidence         WorkloadProfileConfidence `json:"confidence"`
	AdvisoryOnly       bool                      `json:"advisory_only"`
	ProductionMutation bool                      `json:"production_mutation"`
}

type WorkloadCurveEstimate struct {
	SchemaVersion         string              `json:"schema_version"`
	EstimateID            string              `json:"estimate_id"`
	CurveID               string              `json:"curve_id"`
	TargetResourceFactor  float64             `json:"target_resource_factor"`
	EstimatedSafeWorkload *float64            `json:"estimated_safe_workload,omitempty"`
	Status                WorkloadCurveStatus `json:"status"`
	Reason                string              `json:"reason"`
	AdvisoryOnly          bool                `json:"advisory_only"`
	ProductionMutation    bool                `json:"production_mutation"`
}

// BuildWorkloadCurve builds a deterministic piecewise-linear capacity curve
// from benchmark-backed workload profiles. Contradictory evidence is blocked;
// the curve never authorizes infrastructure changes.
func BuildWorkloadCurve(request WorkloadCurveRequest, points []WorkloadCurvePoint) (WorkloadCurve, error) {
	request.ScopeID = strings.TrimSpace(request.ScopeID)
	request.WorkloadUnit = WorkloadUnit(strings.ToLower(strings.TrimSpace(string(request.WorkloadUnit))))
	if !identifierPattern.MatchString(request.ScopeID) || !validWorkloadUnit(request.WorkloadUnit) {
		return WorkloadCurve{}, fmt.Errorf("%w: invalid workload curve identity", ErrInvalidProfile)
	}
	if request.MinimumPoints < 3 || request.MinimumPoints > 64 {
		return WorkloadCurve{}, fmt.Errorf("%w: minimum_points must be within [3,64]", ErrInvalidProfile)
	}
	if len(points) < request.MinimumPoints || len(points) > 64 {
		return WorkloadCurve{}, fmt.Errorf("%w: points do not satisfy minimum_points or maximum size", ErrInvalidProfile)
	}

	normalized := make([]WorkloadCurvePoint, len(points))
	seenPointIDs := make(map[string]struct{}, len(points))
	seenProfileIDs := make(map[string]struct{}, len(points))
	seenFactors := make(map[float64]struct{}, len(points))
	for index, point := range points {
		point.PointID = strings.TrimSpace(point.PointID)
		point.SourceProfileID = strings.TrimSpace(point.SourceProfileID)
		if !identifierPattern.MatchString(point.PointID) {
			return WorkloadCurve{}, fmt.Errorf("%w: invalid curve point_id %q", ErrInvalidProfile, point.PointID)
		}
		if !validWorkloadProfileID(point.SourceProfileID) {
			return WorkloadCurve{}, fmt.Errorf("%w: invalid source_profile_id %q", ErrInvalidProfile, point.SourceProfileID)
		}
		if !finitePositive(point.ResourceFactor) || point.ResourceFactor > 1000 {
			return WorkloadCurve{}, fmt.Errorf("%w: resource_factor is outside supported bounds", ErrInvalidProfile)
		}
		if !finitePositive(point.SafeWorkload) || point.SafeWorkload > 1_000_000_000_000 {
			return WorkloadCurve{}, fmt.Errorf("%w: safe_workload is outside supported bounds", ErrInvalidProfile)
		}
		if !validWorkloadProfileConfidence(point.Confidence) {
			return WorkloadCurve{}, fmt.Errorf("%w: invalid curve point confidence", ErrInvalidProfile)
		}
		if _, duplicate := seenPointIDs[point.PointID]; duplicate {
			return WorkloadCurve{}, fmt.Errorf("%w: duplicate curve point_id %q", ErrInvalidProfile, point.PointID)
		}
		if _, duplicate := seenProfileIDs[point.SourceProfileID]; duplicate {
			return WorkloadCurve{}, fmt.Errorf("%w: duplicate source_profile_id %q", ErrInvalidProfile, point.SourceProfileID)
		}
		if _, duplicate := seenFactors[point.ResourceFactor]; duplicate {
			return WorkloadCurve{}, fmt.Errorf("%w: duplicate resource_factor", ErrInvalidProfile)
		}
		seenPointIDs[point.PointID] = struct{}{}
		seenProfileIDs[point.SourceProfileID] = struct{}{}
		seenFactors[point.ResourceFactor] = struct{}{}
		normalized[index] = point
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].ResourceFactor == normalized[j].ResourceFactor {
			return normalized[i].PointID < normalized[j].PointID
		}
		return normalized[i].ResourceFactor < normalized[j].ResourceFactor
	})

	status := WorkloadCurveReady
	reason := "piecewise_curve_ready"
	for index := 1; index < len(normalized); index++ {
		previous := normalized[index-1].SafeWorkload
		current := normalized[index].SafeWorkload
		if current+floatTolerance(previous, current) < previous {
			status = WorkloadCurveBlocked
			reason = "non_monotonic_capacity_curve"
			break
		}
	}
	confidence := normalized[0].Confidence
	for _, point := range normalized[1:] {
		if workloadProfileConfidenceRank(point.Confidence) < workloadProfileConfidenceRank(confidence) {
			confidence = point.Confidence
		}
	}

	canonical := struct {
		Request WorkloadCurveRequest `json:"request"`
		Points  []WorkloadCurvePoint `json:"points"`
	}{request, normalized}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return WorkloadCurve{}, fmt.Errorf("%w: workload curve fingerprint: %v", ErrInvalidProfile, err)
	}
	digest := sha256.Sum256(encoded)
	return WorkloadCurve{
		SchemaVersion:      WorkloadCurveSchemaV1,
		CurveID:            "wlc-" + hex.EncodeToString(digest[:])[:24],
		ScopeID:            request.ScopeID,
		WorkloadUnit:       request.WorkloadUnit,
		MinimumPoints:      request.MinimumPoints,
		Points:             normalized,
		Status:             status,
		Reason:             reason,
		Confidence:         confidence,
		AdvisoryOnly:       true,
		ProductionMutation: false,
	}, nil
}

// EstimateWorkloadCurve interpolates only inside the observed curve. It never
// extrapolates beyond measured resource factors; callers must collect evidence
// instead of treating an unobserved scale factor as safe capacity.
func EstimateWorkloadCurve(curve WorkloadCurve, targetResourceFactor float64) (WorkloadCurveEstimate, error) {
	if curve.SchemaVersion != WorkloadCurveSchemaV1 || !curve.AdvisoryOnly || curve.ProductionMutation {
		return WorkloadCurveEstimate{}, fmt.Errorf("%w: invalid workload curve evidence", ErrInvalidRecommendation)
	}
	if !finitePositive(targetResourceFactor) || targetResourceFactor > 1000 {
		return WorkloadCurveEstimate{}, fmt.Errorf("%w: target_resource_factor is outside supported bounds", ErrInvalidRecommendation)
	}
	rebuilt, err := BuildWorkloadCurve(
		WorkloadCurveRequest{ScopeID: curve.ScopeID, WorkloadUnit: curve.WorkloadUnit, MinimumPoints: curve.MinimumPoints},
		curve.Points,
	)
	if err != nil {
		return WorkloadCurveEstimate{}, fmt.Errorf("%w: invalid workload curve evidence: %v", ErrInvalidRecommendation, err)
	}
	if rebuilt.CurveID != curve.CurveID || rebuilt.Status != curve.Status || rebuilt.Reason != curve.Reason ||
		rebuilt.Confidence != curve.Confidence {
		return WorkloadCurveEstimate{}, fmt.Errorf("%w: workload curve evidence mismatch", ErrInvalidRecommendation)
	}
	curve = rebuilt
	if curve.Status == WorkloadCurveBlocked {
		return curveEstimate(curve, targetResourceFactor, nil, WorkloadCurveBlocked, "curve_blocked"), nil
	}
	if curve.Status != WorkloadCurveReady {
		return WorkloadCurveEstimate{}, fmt.Errorf("%w: unsupported workload curve state", ErrInvalidRecommendation)
	}

	points := curve.Points
	if targetResourceFactor < points[0].ResourceFactor-floatTolerance(targetResourceFactor, points[0].ResourceFactor) ||
		targetResourceFactor > points[len(points)-1].ResourceFactor+floatTolerance(targetResourceFactor, points[len(points)-1].ResourceFactor) {
		return curveEstimate(curve, targetResourceFactor, nil, WorkloadCurveCollectEvidence, "target_outside_observed_curve"), nil
	}

	for _, point := range points {
		if closeFloat(targetResourceFactor, point.ResourceFactor) {
			value := point.SafeWorkload
			return curveEstimate(curve, targetResourceFactor, &value, WorkloadCurveReady, "observed_curve_point"), nil
		}
	}
	for index := 1; index < len(points); index++ {
		left := points[index-1]
		right := points[index]
		if targetResourceFactor > left.ResourceFactor && targetResourceFactor < right.ResourceFactor {
			fraction := (targetResourceFactor - left.ResourceFactor) / (right.ResourceFactor - left.ResourceFactor)
			value := left.SafeWorkload + fraction*(right.SafeWorkload-left.SafeWorkload)
			if !finitePositive(value) {
				return WorkloadCurveEstimate{}, fmt.Errorf("%w: workload curve interpolation overflow", ErrInvalidRecommendation)
			}
			return curveEstimate(curve, targetResourceFactor, &value, WorkloadCurveReady, "interpolated_within_observed_curve"), nil
		}
	}
	return WorkloadCurveEstimate{}, fmt.Errorf("%w: target could not be resolved on curve", ErrInvalidRecommendation)
}

func curveEstimate(curve WorkloadCurve, target float64, value *float64, status WorkloadCurveStatus, reason string) WorkloadCurveEstimate {
	canonical := struct {
		CurveID string  `json:"curve_id"`
		Target  float64 `json:"target_resource_factor"`
	}{curve.CurveID, target}
	encoded, _ := json.Marshal(canonical)
	digest := sha256.Sum256(encoded)
	return WorkloadCurveEstimate{
		SchemaVersion:         WorkloadCurveEstimateSchemaV1,
		EstimateID:            "wle-" + hex.EncodeToString(digest[:])[:24],
		CurveID:               curve.CurveID,
		TargetResourceFactor:  target,
		EstimatedSafeWorkload: value,
		Status:                status,
		Reason:                reason,
		AdvisoryOnly:          true,
		ProductionMutation:    false,
	}
}

func validWorkloadProfileID(value string) bool {
	if value != strings.ToLower(value) || len(value) != len("wlp-")+24 || !strings.HasPrefix(value, "wlp-") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "wlp-"))
	return err == nil
}

func validWorkloadProfileConfidence(value WorkloadProfileConfidence) bool {
	return value == WorkloadProfileConfidenceLow || value == WorkloadProfileConfidenceMedium || value == WorkloadProfileConfidenceHigh
}

func workloadProfileConfidenceRank(value WorkloadProfileConfidence) int {
	switch value {
	case WorkloadProfileConfidenceHigh:
		return 3
	case WorkloadProfileConfidenceMedium:
		return 2
	case WorkloadProfileConfidenceLow:
		return 1
	default:
		return 0
	}
}
