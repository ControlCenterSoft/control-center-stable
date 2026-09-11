package capacity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"

	"control-center/internal/agent"
)

const PlacementSafetyMarginSchemaV1 = "capacity.placement-safety-margin/v1"

type PlacementSafetyScoreBand string

const (
	PlacementSafetyBlocked  PlacementSafetyScoreBand = "blocked"
	PlacementSafetyBoundary PlacementSafetyScoreBand = "boundary"
	PlacementSafetyHeadroom PlacementSafetyScoreBand = "headroom"
)

const (
	PlacementLimitWorkload   = "workload-safe-reserve"
	PlacementLimitBottleneck = "bottleneck-safe-reserve"
)

type PlacementSafetyMarginCandidate struct {
	NodeID                            string                   `json:"node_id"`
	Eligible                          bool                     `json:"eligible"`
	Reason                            string                   `json:"reason"`
	WorkloadSafeReserve               float64                  `json:"workload_safe_reserve"`
	WorkloadSafeReservePercent        float64                  `json:"workload_safe_reserve_percent"`
	MinimumRequiredReservePercent     float64                  `json:"minimum_required_reserve_percent"`
	WorkloadMarginAboveMinimumPercent float64                  `json:"workload_margin_above_minimum_percent"`
	BottleneckMetric                  agent.CapacityMetric     `json:"bottleneck_metric"`
	BottleneckTargetID                string                   `json:"bottleneck_target_id"`
	BottleneckSafeReservePercent      float64                  `json:"bottleneck_safe_reserve_percent"`
	EffectiveSafetyMarginPercent      float64                  `json:"effective_safety_margin_percent"`
	LimitingDimension                 string                   `json:"limiting_dimension"`
	ScoreBand                         PlacementSafetyScoreBand `json:"score_band"`
	Confidence                        Confidence               `json:"confidence"`
}

type PlacementSafetyMarginEnvelope struct {
	SchemaVersion             string                           `json:"schema_version"`
	EnvelopeID                string                           `json:"envelope_id"`
	AdviceID                  string                           `json:"advice_id"`
	ScopeID                   string                           `json:"scope_id"`
	MinimumNodeReservePercent float64                          `json:"minimum_node_reserve_percent"`
	FailureReserveNodes       int                              `json:"failure_reserve_nodes"`
	FleetSafe                 bool                             `json:"fleet_safe"`
	FleetSafeReserve          float64                          `json:"fleet_safe_reserve"`
	RecommendedNodeID         string                           `json:"recommended_node_id,omitempty"`
	Action                    RecommendationAction             `json:"action"`
	Candidates                []PlacementSafetyMarginCandidate `json:"candidates"`
	AdvisoryOnly              bool                             `json:"advisory_only"`
	PlacementAuthorized       bool                             `json:"placement_authorized"`
	ProductionMutation        bool                             `json:"production_mutation"`
}

// BuildPlacementSafetyMarginEnvelope explains how much safety margin remains
// around each placement candidate. It re-derives the supplied advice first, so
// caller-modified eligibility, reserve, confidence, or recommendation fields are
// rejected rather than being reflected into an authoritative-looking envelope.
// The result is advisory evidence only and cannot authorize placement.
func BuildPlacementSafetyMarginEnvelope(
	request PlacementRequest,
	nodes []NodeProjection,
	advice PlacementAdvice,
) (PlacementSafetyMarginEnvelope, error) {
	expected, err := BuildPlacementAdvice(request, nodes)
	if err != nil {
		return PlacementSafetyMarginEnvelope{}, err
	}
	if !reflect.DeepEqual(expected, advice) {
		return PlacementSafetyMarginEnvelope{}, fmt.Errorf(
			"%w: placement advice does not match current request and projections",
			ErrInvalidRecommendation,
		)
	}

	projectionByNode := make(map[string]NodeProjection, len(nodes))
	for _, node := range nodes {
		projectionByNode[node.NodeID] = node
	}

	margins := make([]PlacementSafetyMarginCandidate, 0, len(advice.Candidates))
	for _, candidate := range advice.Candidates {
		projection, ok := projectionByNode[candidate.NodeID]
		if !ok {
			return PlacementSafetyMarginEnvelope{}, fmt.Errorf(
				"%w: candidate %q has no source projection",
				ErrInvalidRecommendation,
				candidate.NodeID,
			)
		}
		workloadMargin := candidate.SafeReservePercent - request.MinimumNodeReservePercent
		bottleneckMargin := candidate.BottleneckReservePercent
		effectiveMargin := workloadMargin
		limitingDimension := PlacementLimitWorkload
		if bottleneckMargin < workloadMargin-floatTolerance(bottleneckMargin, workloadMargin) {
			effectiveMargin = bottleneckMargin
			limitingDimension = PlacementLimitBottleneck
		}
		band := PlacementSafetyHeadroom
		if !candidate.Eligible || effectiveMargin < -floatTolerance(effectiveMargin) {
			band = PlacementSafetyBlocked
		} else if closeFloat(effectiveMargin, 0) {
			band = PlacementSafetyBoundary
		}
		margins = append(margins, PlacementSafetyMarginCandidate{
			NodeID:                            candidate.NodeID,
			Eligible:                          candidate.Eligible,
			Reason:                            candidate.Reason,
			WorkloadSafeReserve:               candidate.SafeReserve,
			WorkloadSafeReservePercent:        candidate.SafeReservePercent,
			MinimumRequiredReservePercent:     request.MinimumNodeReservePercent,
			WorkloadMarginAboveMinimumPercent: workloadMargin,
			BottleneckMetric:                  projection.BottleneckMetric,
			BottleneckTargetID:                projection.BottleneckTargetID,
			BottleneckSafeReservePercent:      bottleneckMargin,
			EffectiveSafetyMarginPercent:      effectiveMargin,
			LimitingDimension:                 limitingDimension,
			ScoreBand:                         band,
			Confidence:                        candidate.Confidence,
		})
	}

	envelope := PlacementSafetyMarginEnvelope{
		SchemaVersion:             PlacementSafetyMarginSchemaV1,
		AdviceID:                  advice.AdviceID,
		ScopeID:                   advice.ScopeID,
		MinimumNodeReservePercent: request.MinimumNodeReservePercent,
		FailureReserveNodes:       request.FailureReserveNodes,
		FleetSafe:                 advice.FleetAssessment.Safe,
		FleetSafeReserve:          advice.FleetAssessment.SafeReserve,
		RecommendedNodeID:         advice.RecommendedNodeID,
		Action:                    advice.Action,
		Candidates:                margins,
		AdvisoryOnly:              true,
		PlacementAuthorized:       false,
		ProductionMutation:        false,
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return PlacementSafetyMarginEnvelope{}, fmt.Errorf(
			"%w: safety margin fingerprint: %v",
			ErrInvalidRecommendation,
			err,
		)
	}
	digest := sha256.Sum256(encoded)
	envelope.EnvelopeID = "psm-" + hex.EncodeToString(digest[:])[:24]
	return envelope, nil
}

// ValidatePlacementSafetyMarginEnvelope fails closed if any derived or safety
// field was modified after the envelope was built.
func ValidatePlacementSafetyMarginEnvelope(
	envelope PlacementSafetyMarginEnvelope,
	request PlacementRequest,
	nodes []NodeProjection,
	advice PlacementAdvice,
) error {
	expected, err := BuildPlacementSafetyMarginEnvelope(request, nodes, advice)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, envelope) {
		return fmt.Errorf("%w: placement safety margin envelope was modified", ErrInvalidRecommendation)
	}
	return nil
}
