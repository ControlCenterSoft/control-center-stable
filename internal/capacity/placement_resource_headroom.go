package capacity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"

	"control-center/internal/agent"
)

const PlacementResourceHeadroomSchemaV1 = "capacity.placement-resource-headroom/v1"

const PlacementLimitResource = "resource-safe-reserve"

type PlacementResourceHeadroomEntry struct {
	ConstraintID          string                    `json:"constraint_id"`
	Metric                agent.CapacityMetric      `json:"metric"`
	TargetID              string                    `json:"target_id"`
	Unit                  agent.CapacityUnit        `json:"unit"`
	ObservedValue         float64                   `json:"observed_value"`
	SafeLimit             float64                   `json:"safe_limit"`
	TechnicalLimit        float64                   `json:"technical_limit"`
	SafeReserve           float64                   `json:"safe_reserve"`
	SafeReservePercent    float64                   `json:"safe_reserve_percent"`
	Evidence              agent.ObservationEvidence `json:"evidence"`
	ObservedAt            time.Time                 `json:"observed_at"`
	ObservationAgeSeconds uint32                    `json:"observation_age_seconds"`
	ScoreBand             PlacementSafetyScoreBand  `json:"score_band"`
}

type PlacementResourceHeadroomCandidate struct {
	NodeID                            string                           `json:"node_id"`
	Eligible                          bool                             `json:"eligible"`
	Reason                            string                           `json:"reason"`
	WorkloadMarginAboveMinimumPercent float64                          `json:"workload_margin_above_minimum_percent"`
	Resources                         []PlacementResourceHeadroomEntry `json:"resources"`
	LimitingConstraintID              string                           `json:"limiting_constraint_id"`
	LimitingMetric                    agent.CapacityMetric             `json:"limiting_metric"`
	LimitingTargetID                  string                           `json:"limiting_target_id"`
	LimitingResourceReservePercent    float64                          `json:"limiting_resource_reserve_percent"`
	EffectiveSafetyMarginPercent      float64                          `json:"effective_safety_margin_percent"`
	LimitingDimension                 string                           `json:"limiting_dimension"`
	ScoreBand                         PlacementSafetyScoreBand         `json:"score_band"`
	Confidence                        Confidence                       `json:"confidence"`
}

type PlacementResourceHeadroomEnvelope struct {
	SchemaVersion       string                               `json:"schema_version"`
	EnvelopeID          string                               `json:"envelope_id"`
	DerivedSnapshotID   string                               `json:"derived_snapshot_id"`
	PlacementSnapshotID string                               `json:"placement_snapshot_id"`
	AdviceID            string                               `json:"advice_id"`
	ScopeID             string                               `json:"scope_id"`
	EvaluatedAt         time.Time                            `json:"evaluated_at"`
	Candidates          []PlacementResourceHeadroomCandidate `json:"candidates"`
	AdvisoryOnly        bool                                 `json:"advisory_only"`
	PlacementAuthorized bool                                 `json:"placement_authorized"`
	ProductionMutation  bool                                 `json:"production_mutation"`
}

// BuildPlacementResourceHeadroomEnvelope expands the single bottleneck summary
// into all qualified resource constraints for every placement candidate. It
// revalidates the exact profile/telemetry derivations and the derived placement
// snapshot first. The result is explanatory evidence only and never authorizes
// a placement or infrastructure mutation.
func BuildPlacementResourceHeadroomEnvelope(
	request PlacementRequest,
	derived PlacementAdviceDerivedEvidenceSnapshot,
	inputs []PlacementNodeDerivationInput,
	evaluatedAt time.Time,
) (PlacementResourceHeadroomEnvelope, error) {
	if evaluatedAt.IsZero() {
		return PlacementResourceHeadroomEnvelope{}, fmt.Errorf(
			"%w: evaluated_at is required",
			ErrInvalidRecommendation,
		)
	}
	evaluatedAt = evaluatedAt.UTC()
	current, err := CapturePlacementAdviceFromDerivations(request, inputs, evaluatedAt)
	if err != nil {
		return PlacementResourceHeadroomEnvelope{}, err
	}
	if !reflect.DeepEqual(current, derived) {
		return PlacementResourceHeadroomEnvelope{}, fmt.Errorf(
			"%w: derived placement evidence is stale or tampered",
			ErrInvalidRecommendation,
		)
	}

	inputByNode := make(map[string]PlacementNodeDerivationInput, len(inputs))
	for _, input := range inputs {
		inputByNode[input.Evidence.Projection.NodeID] = input
	}

	candidates := make([]PlacementResourceHeadroomCandidate, 0, len(derived.Placement.Advice.Candidates))
	for _, candidate := range derived.Placement.Advice.Candidates {
		input, ok := inputByNode[candidate.NodeID]
		if !ok {
			return PlacementResourceHeadroomEnvelope{}, fmt.Errorf(
				"%w: candidate %q has no derivation input",
				ErrInvalidRecommendation,
				candidate.NodeID,
			)
		}
		resources, err := placementResourceHeadroomEntries(input.Profile, input.Telemetry, evaluatedAt)
		if err != nil {
			return PlacementResourceHeadroomEnvelope{}, err
		}
		limiting := resources[0]
		for _, entry := range resources[1:] {
			if entry.SafeReservePercent < limiting.SafeReservePercent-floatTolerance(entry.SafeReservePercent, limiting.SafeReservePercent) ||
				(closeFloat(entry.SafeReservePercent, limiting.SafeReservePercent) && entry.ConstraintID < limiting.ConstraintID) {
				limiting = entry
			}
		}
		projection := input.Evidence.Projection
		if limiting.Metric != projection.BottleneckMetric ||
			limiting.TargetID != projection.BottleneckTargetID ||
			!closeFloat(limiting.SafeReservePercent, projection.BottleneckReserve) {
			return PlacementResourceHeadroomEnvelope{}, fmt.Errorf(
				"%w: resource vector does not match projection bottleneck for node %q",
				ErrInvalidRecommendation,
				candidate.NodeID,
			)
		}

		workloadMargin := candidate.SafeReservePercent - request.MinimumNodeReservePercent
		effectiveMargin := workloadMargin
		limitingDimension := PlacementLimitWorkload
		if limiting.SafeReservePercent < workloadMargin-floatTolerance(limiting.SafeReservePercent, workloadMargin) {
			effectiveMargin = limiting.SafeReservePercent
			limitingDimension = PlacementLimitResource
		}
		band := PlacementSafetyHeadroom
		if !candidate.Eligible || effectiveMargin < -floatTolerance(effectiveMargin) {
			band = PlacementSafetyBlocked
		} else if closeFloat(effectiveMargin, 0) {
			band = PlacementSafetyBoundary
		}
		candidates = append(candidates, PlacementResourceHeadroomCandidate{
			NodeID:                            candidate.NodeID,
			Eligible:                          candidate.Eligible,
			Reason:                            candidate.Reason,
			WorkloadMarginAboveMinimumPercent: workloadMargin,
			Resources:                         resources,
			LimitingConstraintID:              limiting.ConstraintID,
			LimitingMetric:                    limiting.Metric,
			LimitingTargetID:                  limiting.TargetID,
			LimitingResourceReservePercent:    limiting.SafeReservePercent,
			EffectiveSafetyMarginPercent:      effectiveMargin,
			LimitingDimension:                 limitingDimension,
			ScoreBand:                         band,
			Confidence:                        candidate.Confidence,
		})
	}

	envelope := PlacementResourceHeadroomEnvelope{
		SchemaVersion:       PlacementResourceHeadroomSchemaV1,
		DerivedSnapshotID:   derived.SnapshotID,
		PlacementSnapshotID: derived.Placement.SnapshotID,
		AdviceID:            derived.Placement.Advice.AdviceID,
		ScopeID:             derived.Placement.Advice.ScopeID,
		EvaluatedAt:         evaluatedAt,
		Candidates:          candidates,
		AdvisoryOnly:        true,
		PlacementAuthorized: false,
		ProductionMutation:  false,
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return PlacementResourceHeadroomEnvelope{}, fmt.Errorf(
			"%w: resource headroom fingerprint: %v",
			ErrInvalidRecommendation,
			err,
		)
	}
	digest := sha256.Sum256(encoded)
	envelope.EnvelopeID = "prh-" + hex.EncodeToString(digest[:])[:24]
	return envelope, nil
}

func ValidatePlacementResourceHeadroomEnvelope(
	envelope PlacementResourceHeadroomEnvelope,
	request PlacementRequest,
	derived PlacementAdviceDerivedEvidenceSnapshot,
	inputs []PlacementNodeDerivationInput,
	evaluatedAt time.Time,
) error {
	expected, err := BuildPlacementResourceHeadroomEnvelope(request, derived, inputs, evaluatedAt)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, envelope) {
		return fmt.Errorf("%w: placement resource headroom envelope was modified", ErrInvalidRecommendation)
	}
	return nil
}

func placementResourceHeadroomEntries(
	profile Profile,
	telemetry NodeProjectionTelemetry,
	evaluatedAt time.Time,
) ([]PlacementResourceHeadroomEntry, error) {
	normalizedProfile, err := NormalizeProfile(profile)
	if err != nil {
		return nil, err
	}
	canonicalTelemetry, err := normalizeNodeProjectionTelemetry(telemetry, normalizedProfile, evaluatedAt)
	if err != nil {
		return nil, err
	}
	constraintByKey := make(map[string]Constraint, len(normalizedProfile.Constraints))
	for _, constraint := range normalizedProfile.Constraints {
		constraintByKey[projectionConstraintKey(constraint.Metric, constraint.TargetID)] = constraint
	}
	entries := make([]PlacementResourceHeadroomEntry, 0, len(canonicalTelemetry.Observations))
	for _, observation := range canonicalTelemetry.Observations {
		constraint := constraintByKey[projectionConstraintKey(observation.Metric, observation.TargetID)]
		if err := validateProjectionObservation(constraint, observation, evaluatedAt); err != nil {
			return nil, err
		}
		reserve := constraint.SafeLimit - observation.Value
		reservePercent := reserve / constraint.SafeLimit * 100
		band := PlacementSafetyHeadroom
		if reserve < -floatTolerance(reserve) {
			band = PlacementSafetyBlocked
		} else if closeFloat(reserve, 0) {
			band = PlacementSafetyBoundary
		}
		age := evaluatedAt.Sub(observation.ObservedAt)
		if age < 0 {
			return nil, fmt.Errorf("%w: invalid observation age", ErrInvalidRecommendation)
		}
		entries = append(entries, PlacementResourceHeadroomEntry{
			ConstraintID:          constraint.ID,
			Metric:                observation.Metric,
			TargetID:              observation.TargetID,
			Unit:                  observation.Unit,
			ObservedValue:         observation.Value,
			SafeLimit:             constraint.SafeLimit,
			TechnicalLimit:        constraint.TechnicalLimit,
			SafeReserve:           reserve,
			SafeReservePercent:    reservePercent,
			Evidence:              observation.Evidence,
			ObservedAt:            observation.ObservedAt,
			ObservationAgeSeconds: uint32(age / time.Second),
			ScoreBand:             band,
		})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: resource headroom requires constraints", ErrInvalidRecommendation)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ConstraintID < entries[j].ConstraintID })
	return entries, nil
}
