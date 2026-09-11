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

const PlacementResourceHeadroomFreshnessSchemaV1 = "capacity.placement-resource-headroom-freshness/v1"

type PlacementResourceEvidenceStatus string

const (
	PlacementResourceEvidenceCurrent  PlacementResourceEvidenceStatus = "current"
	PlacementResourceEvidenceDegraded PlacementResourceEvidenceStatus = "degraded-evidence"
	PlacementResourceEvidenceStale    PlacementResourceEvidenceStatus = "stale"
)

type PlacementResourceHeadroomFreshnessCandidate struct {
	NodeID                       string                          `json:"node_id"`
	Status                       PlacementResourceEvidenceStatus `json:"status"`
	Reason                       string                          `json:"reason"`
	OldestObservationAgeSeconds  uint64                          `json:"oldest_observation_age_seconds"`
	MinimumFreshnessRemainingSec int64                           `json:"minimum_freshness_remaining_seconds"`
	StaleConstraintIDs           []string                        `json:"stale_constraint_ids"`
	NonMeasuredConstraintIDs     []string                        `json:"non_measured_constraint_ids"`
	Confidence                   Confidence                      `json:"confidence"`
	AdviceReusable               bool                            `json:"advice_reusable"`
	RecommendedAction            string                          `json:"recommended_action"`
}

type PlacementResourceHeadroomFreshnessGate struct {
	SchemaVersion        string                                        `json:"schema_version"`
	GateID               string                                        `json:"gate_id"`
	HeadroomEnvelopeID   string                                        `json:"headroom_envelope_id"`
	DerivedSnapshotID    string                                        `json:"derived_snapshot_id"`
	PlacementSnapshotID  string                                        `json:"placement_snapshot_id"`
	AdviceID             string                                        `json:"advice_id"`
	ScopeID              string                                        `json:"scope_id"`
	HeadroomEvaluatedAt  time.Time                                     `json:"headroom_evaluated_at"`
	CheckedAt            time.Time                                     `json:"checked_at"`
	Candidates           []PlacementResourceHeadroomFreshnessCandidate `json:"candidates"`
	AllCurrent           bool                                          `json:"all_current"`
	AdviceReusePermitted bool                                          `json:"advice_reuse_permitted"`
	RecommendedAction    string                                        `json:"recommended_action"`
	AdvisoryOnly         bool                                          `json:"advisory_only"`
	PlacementAuthorized  bool                                          `json:"placement_authorized"`
	ProductionMutation   bool                                          `json:"production_mutation"`
}

// BuildPlacementResourceHeadroomFreshnessGate determines whether a sealed
// multi-resource headroom envelope is still fresh enough for advisory reuse.
// It revalidates the exact envelope at its original evaluation time, then
// applies each profile constraint's own freshness budget at checkedAt. Any
// stale, non-measured, or low-confidence evidence requires new evidence. The
// gate is evidence only and never authorizes placement or infrastructure
// mutation.
func BuildPlacementResourceHeadroomFreshnessGate(
	envelope PlacementResourceHeadroomEnvelope,
	request PlacementRequest,
	derived PlacementAdviceDerivedEvidenceSnapshot,
	inputs []PlacementNodeDerivationInput,
	checkedAt time.Time,
) (PlacementResourceHeadroomFreshnessGate, error) {
	if checkedAt.IsZero() {
		return PlacementResourceHeadroomFreshnessGate{}, fmt.Errorf(
			"%w: checked_at is required",
			ErrInvalidRecommendation,
		)
	}
	checkedAt = checkedAt.UTC()
	if checkedAt.Before(envelope.EvaluatedAt) {
		return PlacementResourceHeadroomFreshnessGate{}, fmt.Errorf(
			"%w: checked_at precedes headroom evaluation",
			ErrInvalidRecommendation,
		)
	}
	if err := ValidatePlacementResourceHeadroomEnvelope(
		envelope,
		request,
		derived,
		inputs,
		envelope.EvaluatedAt,
	); err != nil {
		return PlacementResourceHeadroomFreshnessGate{}, err
	}

	inputByNode := make(map[string]PlacementNodeDerivationInput, len(inputs))
	for _, input := range inputs {
		inputByNode[input.Evidence.Projection.NodeID] = input
	}

	candidates := make([]PlacementResourceHeadroomFreshnessCandidate, 0, len(envelope.Candidates))
	allCurrent := true
	for _, candidate := range envelope.Candidates {
		input, ok := inputByNode[candidate.NodeID]
		if !ok {
			return PlacementResourceHeadroomFreshnessGate{}, fmt.Errorf(
				"%w: candidate %q has no derivation input",
				ErrInvalidRecommendation,
				candidate.NodeID,
			)
		}
		profile, err := NormalizeProfile(input.Profile)
		if err != nil {
			return PlacementResourceHeadroomFreshnessGate{}, err
		}
		assessment, err := classifyPlacementResourceHeadroomCandidate(candidate, profile, checkedAt)
		if err != nil {
			return PlacementResourceHeadroomFreshnessGate{}, err
		}
		if assessment.Status != PlacementResourceEvidenceCurrent {
			allCurrent = false
		}
		candidates = append(candidates, assessment)
	}

	gate := PlacementResourceHeadroomFreshnessGate{
		SchemaVersion:        PlacementResourceHeadroomFreshnessSchemaV1,
		HeadroomEnvelopeID:   envelope.EnvelopeID,
		DerivedSnapshotID:    envelope.DerivedSnapshotID,
		PlacementSnapshotID:  envelope.PlacementSnapshotID,
		AdviceID:             envelope.AdviceID,
		ScopeID:              envelope.ScopeID,
		HeadroomEvaluatedAt:  envelope.EvaluatedAt,
		CheckedAt:            checkedAt,
		Candidates:           candidates,
		AllCurrent:           allCurrent,
		AdviceReusePermitted: allCurrent,
		RecommendedAction:    "collect-evidence",
		AdvisoryOnly:         true,
		PlacementAuthorized:  false,
		ProductionMutation:   false,
	}
	if allCurrent {
		gate.RecommendedAction = "none"
	}
	gate.GateID = placementResourceHeadroomFreshnessGateID(gate)
	return gate, nil
}

// ValidatePlacementResourceHeadroomFreshnessGate rebuilds the gate from exact
// source evidence and rejects any change to its lineage, verdict, or safety
// boundary.
func ValidatePlacementResourceHeadroomFreshnessGate(
	gate PlacementResourceHeadroomFreshnessGate,
	envelope PlacementResourceHeadroomEnvelope,
	request PlacementRequest,
	derived PlacementAdviceDerivedEvidenceSnapshot,
	inputs []PlacementNodeDerivationInput,
	checkedAt time.Time,
) error {
	expected, err := BuildPlacementResourceHeadroomFreshnessGate(
		envelope,
		request,
		derived,
		inputs,
		checkedAt,
	)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, gate) {
		return fmt.Errorf(
			"%w: placement resource headroom freshness gate was modified",
			ErrInvalidRecommendation,
		)
	}
	return nil
}

func classifyPlacementResourceHeadroomCandidate(
	candidate PlacementResourceHeadroomCandidate,
	profile Profile,
	checkedAt time.Time,
) (PlacementResourceHeadroomFreshnessCandidate, error) {
	constraintByID := make(map[string]Constraint, len(profile.Constraints))
	for _, constraint := range profile.Constraints {
		constraintByID[constraint.ID] = constraint
	}
	if len(candidate.Resources) != len(constraintByID) {
		return PlacementResourceHeadroomFreshnessCandidate{}, fmt.Errorf(
			"%w: candidate %q resource vector is incomplete",
			ErrInvalidRecommendation,
			candidate.NodeID,
		)
	}

	stale := make([]string, 0)
	nonMeasured := make([]string, 0)
	oldestAge := uint64(0)
	minimumRemaining := int64(0)
	remainingSet := false
	seen := make(map[string]struct{}, len(candidate.Resources))
	for _, resource := range candidate.Resources {
		constraint, ok := constraintByID[resource.ConstraintID]
		if !ok {
			return PlacementResourceHeadroomFreshnessCandidate{}, fmt.Errorf(
				"%w: candidate %q has unknown constraint %q",
				ErrInvalidRecommendation,
				candidate.NodeID,
				resource.ConstraintID,
			)
		}
		if _, duplicate := seen[resource.ConstraintID]; duplicate {
			return PlacementResourceHeadroomFreshnessCandidate{}, fmt.Errorf(
				"%w: candidate %q repeats constraint %q",
				ErrInvalidRecommendation,
				candidate.NodeID,
				resource.ConstraintID,
			)
		}
		seen[resource.ConstraintID] = struct{}{}

		age := checkedAt.Sub(resource.ObservedAt)
		if age < 0 {
			return PlacementResourceHeadroomFreshnessCandidate{}, fmt.Errorf(
				"%w: candidate %q contains future observation",
				ErrInvalidRecommendation,
				candidate.NodeID,
			)
		}
		ageSeconds := ceilDurationSeconds(age)
		if ageSeconds > oldestAge {
			oldestAge = ageSeconds
		}
		remaining := int64(constraint.MaxObservationAgeSeconds) - int64(ageSeconds)
		if !remainingSet || remaining < minimumRemaining {
			minimumRemaining = remaining
			remainingSet = true
		}
		if ageSeconds > uint64(constraint.MaxObservationAgeSeconds) {
			stale = append(stale, constraint.ID)
		}
		if resource.Evidence != agent.EvidenceMeasured {
			nonMeasured = append(nonMeasured, constraint.ID)
		}
	}
	sort.Strings(stale)
	sort.Strings(nonMeasured)

	result := PlacementResourceHeadroomFreshnessCandidate{
		NodeID:                       candidate.NodeID,
		Status:                       PlacementResourceEvidenceCurrent,
		Reason:                       "exact-current-measured-headroom",
		OldestObservationAgeSeconds:  oldestAge,
		MinimumFreshnessRemainingSec: minimumRemaining,
		StaleConstraintIDs:           stale,
		NonMeasuredConstraintIDs:     nonMeasured,
		Confidence:                   candidate.Confidence,
		AdviceReusable:               true,
		RecommendedAction:            "none",
	}
	if len(stale) > 0 {
		result.Status = PlacementResourceEvidenceStale
		result.Reason = "constraint-observation-stale"
		result.AdviceReusable = false
		result.RecommendedAction = "collect-current-telemetry"
		return result, nil
	}
	if len(nonMeasured) > 0 || candidate.Confidence.Level == ConfidenceLow {
		result.Status = PlacementResourceEvidenceDegraded
		result.Reason = "non-measured-constraint-evidence"
		if len(nonMeasured) == 0 {
			result.Reason = "low-confidence-placement-evidence"
		}
		result.AdviceReusable = false
		result.RecommendedAction = "collect-measured-evidence"
	}
	return result, nil
}

func ceilDurationSeconds(value time.Duration) uint64 {
	seconds := uint64(value / time.Second)
	if value%time.Second != 0 {
		seconds++
	}
	return seconds
}

func placementResourceHeadroomFreshnessGateID(gate PlacementResourceHeadroomFreshnessGate) string {
	canonical := gate
	canonical.GateID = ""
	encoded, _ := json.Marshal(canonical)
	digest := sha256.Sum256(encoded)
	return "prhg-" + hex.EncodeToString(digest[:])[:24]
}
