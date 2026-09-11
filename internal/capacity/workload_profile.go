package capacity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const WorkloadProfileSchemaV1 = "capacity.workload-profile/v1"

type WorkloadProfileStatus string

const (
	WorkloadProfileReady           WorkloadProfileStatus = "ready"
	WorkloadProfileCollectEvidence WorkloadProfileStatus = "collect-evidence"
	WorkloadProfileBlocked         WorkloadProfileStatus = "blocked"
)

type WorkloadProfileConfidence string

const (
	WorkloadProfileConfidenceLow    WorkloadProfileConfidence = "low"
	WorkloadProfileConfidenceMedium WorkloadProfileConfidence = "medium"
	WorkloadProfileConfidenceHigh   WorkloadProfileConfidence = "high"
)

type WorkloadProfileSample struct {
	SampleID               string  `json:"sample_id"`
	Workload               float64 `json:"workload"`
	UtilizationPercent     float64 `json:"utilization_percent"`
	ErrorRatePercent       float64 `json:"error_rate_percent"`
	P95LatencyMilliseconds float64 `json:"p95_latency_milliseconds"`
}

type WorkloadProfileRequest struct {
	ScopeID                   string       `json:"scope_id"`
	WorkloadUnit              WorkloadUnit `json:"workload_unit"`
	MinimumSamples            int          `json:"minimum_samples"`
	TargetUtilizationPercent  float64      `json:"target_utilization_percent"`
	MaxErrorRatePercent       float64      `json:"max_error_rate_percent"`
	MaxP95LatencyMilliseconds float64      `json:"max_p95_latency_milliseconds"`
	SafetyReservePercent      float64      `json:"safety_reserve_percent"`
}

type WorkloadProfileResult struct {
	SchemaVersion             string                    `json:"schema_version"`
	ProfileID                 string                    `json:"profile_id"`
	ScopeID                   string                    `json:"scope_id"`
	WorkloadUnit              WorkloadUnit              `json:"workload_unit"`
	SampleCount               int                       `json:"sample_count"`
	SLOConformingSamples      int                       `json:"slo_conforming_samples"`
	ObservedSafeBoundary      float64                   `json:"observed_safe_boundary"`
	RecommendedSafeWorkload   float64                   `json:"recommended_safe_workload"`
	ObservedTechnicalBoundary float64                   `json:"observed_technical_boundary"`
	BoundaryObserved          bool                      `json:"boundary_observed"`
	Confidence                WorkloadProfileConfidence `json:"confidence"`
	Status                    WorkloadProfileStatus     `json:"status"`
	Reason                    string                    `json:"reason"`
	AdvisoryOnly              bool                      `json:"advisory_only"`
	ProductionMutation        bool                      `json:"production_mutation"`
}

// BuildWorkloadProfile turns bounded load-test or telemetry samples into
// deterministic advisory capacity evidence. It does not persist a profile or
// authorize placement, resize, migration, procurement, or any other mutation.
func BuildWorkloadProfile(request WorkloadProfileRequest, samples []WorkloadProfileSample) (WorkloadProfileResult, error) {
	request.ScopeID = strings.TrimSpace(request.ScopeID)
	request.WorkloadUnit = WorkloadUnit(strings.ToLower(strings.TrimSpace(string(request.WorkloadUnit))))
	if !identifierPattern.MatchString(request.ScopeID) || !validWorkloadUnit(request.WorkloadUnit) {
		return WorkloadProfileResult{}, fmt.Errorf("%w: invalid workload profile identity", ErrInvalidProfile)
	}
	if request.MinimumSamples < 3 || request.MinimumSamples > 4096 {
		return WorkloadProfileResult{}, fmt.Errorf("%w: minimum_samples must be within [3,4096]", ErrInvalidProfile)
	}
	if !finitePositive(request.TargetUtilizationPercent) || request.TargetUtilizationPercent > 100 {
		return WorkloadProfileResult{}, fmt.Errorf("%w: target_utilization_percent must be within (0,100]", ErrInvalidProfile)
	}
	if !finite(request.MaxErrorRatePercent) || request.MaxErrorRatePercent < 0 || request.MaxErrorRatePercent > 100 {
		return WorkloadProfileResult{}, fmt.Errorf("%w: max_error_rate_percent must be within [0,100]", ErrInvalidProfile)
	}
	if !finitePositive(request.MaxP95LatencyMilliseconds) || request.MaxP95LatencyMilliseconds > 86_400_000 {
		return WorkloadProfileResult{}, fmt.Errorf("%w: max_p95_latency_milliseconds is outside supported bounds", ErrInvalidProfile)
	}
	if !finitePositive(request.SafetyReservePercent) || request.SafetyReservePercent > 90 {
		return WorkloadProfileResult{}, fmt.Errorf("%w: safety_reserve_percent must be within (0,90]", ErrInvalidProfile)
	}
	if len(samples) < request.MinimumSamples || len(samples) > 4096 {
		return WorkloadProfileResult{}, fmt.Errorf("%w: samples do not satisfy minimum_samples or maximum size", ErrInvalidProfile)
	}

	normalized := make([]WorkloadProfileSample, len(samples))
	seenIDs := make(map[string]struct{}, len(samples))
	seenWorkloads := make(map[float64]struct{}, len(samples))
	for i, sample := range samples {
		sample.SampleID = strings.TrimSpace(sample.SampleID)
		if !identifierPattern.MatchString(sample.SampleID) {
			return WorkloadProfileResult{}, fmt.Errorf("%w: invalid sample_id %q", ErrInvalidProfile, sample.SampleID)
		}
		if _, duplicate := seenIDs[sample.SampleID]; duplicate {
			return WorkloadProfileResult{}, fmt.Errorf("%w: duplicate sample_id %q", ErrInvalidProfile, sample.SampleID)
		}
		if !finitePositive(sample.Workload) || sample.Workload > 1_000_000_000_000 {
			return WorkloadProfileResult{}, fmt.Errorf("%w: workload is outside supported bounds", ErrInvalidProfile)
		}
		if _, duplicate := seenWorkloads[sample.Workload]; duplicate {
			return WorkloadProfileResult{}, fmt.Errorf("%w: duplicate workload sample", ErrInvalidProfile)
		}
		if !finite(sample.UtilizationPercent) || sample.UtilizationPercent < 0 || sample.UtilizationPercent > 100 ||
			!finite(sample.ErrorRatePercent) || sample.ErrorRatePercent < 0 || sample.ErrorRatePercent > 100 ||
			!finitePositive(sample.P95LatencyMilliseconds) || sample.P95LatencyMilliseconds > 86_400_000 {
			return WorkloadProfileResult{}, fmt.Errorf("%w: invalid workload profile sample %q", ErrInvalidProfile, sample.SampleID)
		}
		seenIDs[sample.SampleID] = struct{}{}
		seenWorkloads[sample.Workload] = struct{}{}
		normalized[i] = sample
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].Workload == normalized[j].Workload {
			return normalized[i].SampleID < normalized[j].SampleID
		}
		return normalized[i].Workload < normalized[j].Workload
	})

	healthy := func(sample WorkloadProfileSample) bool {
		return sample.UtilizationPercent <= request.TargetUtilizationPercent+floatTolerance(sample.UtilizationPercent, request.TargetUtilizationPercent) &&
			sample.ErrorRatePercent <= request.MaxErrorRatePercent+floatTolerance(sample.ErrorRatePercent, request.MaxErrorRatePercent) &&
			sample.P95LatencyMilliseconds <= request.MaxP95LatencyMilliseconds+floatTolerance(sample.P95LatencyMilliseconds, request.MaxP95LatencyMilliseconds)
	}

	healthyCount := 0
	highestHealthy := 0.0
	firstFailure := 0.0
	failureSeen := false
	inconsistent := false
	for _, sample := range normalized {
		if healthy(sample) {
			healthyCount++
			if failureSeen {
				inconsistent = true
			}
			highestHealthy = sample.Workload
			continue
		}
		if !failureSeen {
			firstFailure = sample.Workload
			failureSeen = true
		}
	}

	status := WorkloadProfileReady
	reason := "slo_boundary_observed"
	boundaryObserved := failureSeen
	technicalBoundary := firstFailure
	if technicalBoundary == 0 {
		technicalBoundary = normalized[len(normalized)-1].Workload
	}
	if healthyCount == 0 {
		status = WorkloadProfileBlocked
		reason = "no_slo_conforming_sample"
		boundaryObserved = false
		highestHealthy = 0
		technicalBoundary = normalized[0].Workload
	} else if inconsistent {
		status = WorkloadProfileBlocked
		reason = "non_monotonic_slo_evidence"
		boundaryObserved = true
	} else if !failureSeen {
		status = WorkloadProfileCollectEvidence
		reason = "saturation_boundary_not_observed"
	}

	recommended := highestHealthy * (1 - request.SafetyReservePercent/100)
	if recommended < 0 || !finite(recommended) {
		return WorkloadProfileResult{}, fmt.Errorf("%w: workload profile aggregate overflow", ErrInvalidProfile)
	}
	confidence := WorkloadProfileConfidenceLow
	if status != WorkloadProfileBlocked {
		if boundaryObserved && len(normalized) >= 20 && healthyCount >= 8 {
			confidence = WorkloadProfileConfidenceHigh
		} else if boundaryObserved && len(normalized) >= 8 && healthyCount >= 3 {
			confidence = WorkloadProfileConfidenceMedium
		} else if !boundaryObserved && len(normalized) >= 20 {
			confidence = WorkloadProfileConfidenceMedium
		}
	}

	canonical := struct {
		Request WorkloadProfileRequest  `json:"request"`
		Samples []WorkloadProfileSample `json:"samples"`
	}{request, normalized}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return WorkloadProfileResult{}, fmt.Errorf("%w: workload profile fingerprint: %v", ErrInvalidProfile, err)
	}
	digest := sha256.Sum256(encoded)

	return WorkloadProfileResult{
		SchemaVersion:             WorkloadProfileSchemaV1,
		ProfileID:                 "wlp-" + hex.EncodeToString(digest[:])[:24],
		ScopeID:                   request.ScopeID,
		WorkloadUnit:              request.WorkloadUnit,
		SampleCount:               len(normalized),
		SLOConformingSamples:      healthyCount,
		ObservedSafeBoundary:      highestHealthy,
		RecommendedSafeWorkload:   recommended,
		ObservedTechnicalBoundary: technicalBoundary,
		BoundaryObserved:          boundaryObserved,
		Confidence:                confidence,
		Status:                    status,
		Reason:                    reason,
		AdvisoryOnly:              true,
		ProductionMutation:        false,
	}, nil
}
