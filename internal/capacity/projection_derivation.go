package capacity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"control-center/internal/agent"
	"control-center/internal/corecontracts"
)

const (
	NodeProjectionDerivationSchemaV1 = "capacity.node-projection-derivation/v1"
	maxProjectionTelemetryAgeSeconds = 15 * 60
	maxProjectionRevisionLength      = 512
)

// NodeProjectionTelemetry is one bounded telemetry snapshot used to derive a
// planner NodeProjection. The caller supplies measured state, never a derived
// capacity or bottleneck result.
type NodeProjectionTelemetry struct {
	SnapshotID      string                      `json:"snapshot_id"`
	Revision        string                      `json:"revision"`
	NodeID          string                      `json:"node_id"`
	Role            corecontracts.NodeRole      `json:"role"`
	Healthy         bool                        `json:"healthy"`
	ObservedAt      time.Time                   `json:"observed_at"`
	CurrentWorkload float64                     `json:"current_workload"`
	WorkloadUnit    WorkloadUnit                `json:"workload_unit"`
	Observations    []agent.CapacityObservation `json:"observations"`
}

// NodeProjectionDerivationEvidence binds one planner projection to the exact
// CapacityProfile revision and telemetry snapshot from which it was derived.
// It is advisory evidence only and cannot authorize placement or host changes.
type NodeProjectionDerivationEvidence struct {
	SchemaVersion          string         `json:"schema_version"`
	DerivationID           string         `json:"derivation_id"`
	ProfileObjectID        string         `json:"profile_object_id"`
	ProfileGeneration      uint64         `json:"profile_generation"`
	ProfileResourceVersion string         `json:"profile_resource_version"`
	TelemetrySnapshotID    string         `json:"telemetry_snapshot_id"`
	TelemetryRevision      string         `json:"telemetry_revision"`
	TelemetryObservedAt    time.Time      `json:"telemetry_observed_at"`
	EvaluatedAt            time.Time      `json:"evaluated_at"`
	Projection             NodeProjection `json:"projection"`
	AdvisoryOnly           bool           `json:"advisory_only"`
	ProductionMutation     bool           `json:"production_mutation"`
}

// DeriveNodeProjection computes a deterministic projection from a qualified
// node CapacityProfile plus one complete current telemetry snapshot. Every
// profile constraint must have exactly one matching current observation, so a
// caller cannot hide a tighter constraint by selecting its preferred metric.
func DeriveNodeProjection(
	profile Profile,
	telemetry NodeProjectionTelemetry,
	evaluatedAt time.Time,
) (NodeProjectionDerivationEvidence, error) {
	normalizedProfile, err := NormalizeProfile(profile)
	if err != nil {
		return NodeProjectionDerivationEvidence{}, err
	}
	if normalizedProfile.Subject.Kind != SubjectNode {
		return NodeProjectionDerivationEvidence{}, fmt.Errorf("%w: projection profile must target one node", ErrInvalidRecommendation)
	}

	canonicalTelemetry, err := normalizeNodeProjectionTelemetry(telemetry, normalizedProfile, evaluatedAt)
	if err != nil {
		return NodeProjectionDerivationEvidence{}, err
	}
	evaluatedAt = evaluatedAt.UTC()

	constraintByKey := make(map[string]Constraint, len(normalizedProfile.Constraints))
	for _, constraint := range normalizedProfile.Constraints {
		constraintByKey[projectionConstraintKey(constraint.Metric, constraint.TargetID)] = constraint
	}

	bottleneckConstraint := Constraint{}
	bottleneckReserve := 0.0
	for index, observation := range canonicalTelemetry.Observations {
		constraint := constraintByKey[projectionConstraintKey(observation.Metric, observation.TargetID)]
		if err := validateProjectionObservation(constraint, observation, evaluatedAt); err != nil {
			return NodeProjectionDerivationEvidence{}, err
		}
		reserve := (constraint.SafeLimit - observation.Value) / constraint.SafeLimit * 100
		if index == 0 || reserve < bottleneckReserve-floatTolerance(reserve, bottleneckReserve) ||
			(closeFloat(reserve, bottleneckReserve) && constraint.ID < bottleneckConstraint.ID) {
			bottleneckConstraint = constraint
			bottleneckReserve = reserve
		}
	}

	projection := NodeProjection{
		NodeID:             canonicalTelemetry.NodeID,
		Role:               canonicalTelemetry.Role,
		Healthy:            canonicalTelemetry.Healthy,
		CurrentWorkload:    canonicalTelemetry.CurrentWorkload,
		SafeCapacity:       normalizedProfile.SafeCapacity,
		TechnicalLimit:     normalizedProfile.TechnicalLimit,
		Confidence:         normalizedProfile.Confidence,
		BottleneckMetric:   bottleneckConstraint.Metric,
		BottleneckTargetID: bottleneckConstraint.TargetID,
		BottleneckReserve:  bottleneckReserve,
	}

	evidence := NodeProjectionDerivationEvidence{
		SchemaVersion:          NodeProjectionDerivationSchemaV1,
		ProfileObjectID:        normalizedProfile.ObjectID,
		ProfileGeneration:      normalizedProfile.Generation,
		ProfileResourceVersion: normalizedProfile.ResourceVersion,
		TelemetrySnapshotID:    canonicalTelemetry.SnapshotID,
		TelemetryRevision:      canonicalTelemetry.Revision,
		TelemetryObservedAt:    canonicalTelemetry.ObservedAt,
		EvaluatedAt:            evaluatedAt,
		Projection:             projection,
		AdvisoryOnly:           true,
		ProductionMutation:     false,
	}
	evidence.DerivationID, err = nodeProjectionDerivationID(evidence, canonicalTelemetry.Observations)
	if err != nil {
		return NodeProjectionDerivationEvidence{}, err
	}
	return evidence, nil
}

// ValidateNodeProjectionDerivation reconstructs the projection from current
// exact inputs and rejects profile, telemetry, projection, or policy drift.
func ValidateNodeProjectionDerivation(
	saved NodeProjectionDerivationEvidence,
	profile Profile,
	telemetry NodeProjectionTelemetry,
	evaluatedAt time.Time,
) error {
	if saved.SchemaVersion != NodeProjectionDerivationSchemaV1 || !saved.AdvisoryOnly || saved.ProductionMutation {
		return fmt.Errorf("%w: unsafe node projection derivation evidence", ErrInvalidRecommendation)
	}
	if saved.TelemetryObservedAt.Location() != time.UTC || saved.EvaluatedAt.Location() != time.UTC {
		return fmt.Errorf("%w: derivation timestamps must be canonical UTC", ErrInvalidRecommendation)
	}
	current, err := DeriveNodeProjection(profile, telemetry, evaluatedAt)
	if err != nil {
		return err
	}
	if saved != current {
		return fmt.Errorf("%w: node projection derivation evidence is stale or tampered", ErrInvalidRecommendation)
	}
	return nil
}

func normalizeNodeProjectionTelemetry(
	telemetry NodeProjectionTelemetry,
	profile Profile,
	evaluatedAt time.Time,
) (NodeProjectionTelemetry, error) {
	telemetry.SnapshotID = strings.TrimSpace(telemetry.SnapshotID)
	telemetry.Revision = strings.TrimSpace(telemetry.Revision)
	telemetry.NodeID = strings.TrimSpace(telemetry.NodeID)
	telemetry.WorkloadUnit = WorkloadUnit(strings.ToLower(strings.TrimSpace(string(telemetry.WorkloadUnit))))
	if !identifierPattern.MatchString(telemetry.SnapshotID) || !identifierPattern.MatchString(telemetry.NodeID) ||
		!validProjectionRevision(telemetry.Revision) {
		return NodeProjectionTelemetry{}, fmt.Errorf("%w: invalid projection telemetry identity", ErrInvalidRecommendation)
	}
	if telemetry.NodeID != profile.Subject.ID {
		return NodeProjectionTelemetry{}, fmt.Errorf("%w: telemetry node does not match profile subject", ErrInvalidRecommendation)
	}
	telemetry.Role = corecontracts.NodeRole(strings.ToLower(strings.TrimSpace(string(telemetry.Role))))
	if !telemetry.Role.Valid() {
		return NodeProjectionTelemetry{}, fmt.Errorf("%w: invalid projection node role", ErrInvalidRecommendation)
	}
	if telemetry.WorkloadUnit != profile.WorkloadUnit {
		return NodeProjectionTelemetry{}, fmt.Errorf("%w: workload unit does not match profile", ErrInvalidRecommendation)
	}
	if !finite(telemetry.CurrentWorkload) || telemetry.CurrentWorkload < 0 {
		return NodeProjectionTelemetry{}, fmt.Errorf("%w: current workload must be finite and non-negative", ErrInvalidRecommendation)
	}
	if telemetry.ObservedAt.IsZero() || evaluatedAt.IsZero() {
		return NodeProjectionTelemetry{}, fmt.Errorf("%w: telemetry and evaluation timestamps are required", ErrInvalidRecommendation)
	}
	telemetry.ObservedAt = telemetry.ObservedAt.UTC()
	evaluatedAt = evaluatedAt.UTC()
	if evaluatedAt.Before(telemetry.ObservedAt) ||
		evaluatedAt.Sub(telemetry.ObservedAt) > maxProjectionTelemetryAgeSeconds*time.Second {
		return NodeProjectionTelemetry{}, fmt.Errorf("%w: projection telemetry snapshot is stale or from the future", ErrInvalidRecommendation)
	}
	if len(telemetry.Observations) != len(profile.Constraints) {
		return NodeProjectionTelemetry{}, fmt.Errorf("%w: telemetry must cover every profile constraint exactly once", ErrInvalidRecommendation)
	}

	canonical := make([]agent.CapacityObservation, 0, len(telemetry.Observations))
	seen := make(map[string]struct{}, len(telemetry.Observations))
	profileConstraints := make(map[string]struct{}, len(profile.Constraints))
	for _, constraint := range profile.Constraints {
		profileConstraints[projectionConstraintKey(constraint.Metric, constraint.TargetID)] = struct{}{}
	}
	for _, raw := range telemetry.Observations {
		observation, err := normalizeObservation(raw)
		if err != nil {
			return NodeProjectionTelemetry{}, fmt.Errorf("%w: telemetry observation: %v", ErrInvalidRecommendation, err)
		}
		key := projectionConstraintKey(observation.Metric, observation.TargetID)
		if _, exists := profileConstraints[key]; !exists {
			return NodeProjectionTelemetry{}, fmt.Errorf("%w: telemetry references an unprofiled constraint", ErrInvalidRecommendation)
		}
		if _, duplicate := seen[key]; duplicate {
			return NodeProjectionTelemetry{}, fmt.Errorf("%w: duplicate telemetry observation for one constraint", ErrInvalidRecommendation)
		}
		if observation.ObservedAt.After(telemetry.ObservedAt) {
			return NodeProjectionTelemetry{}, fmt.Errorf("%w: observation follows telemetry snapshot time", ErrInvalidRecommendation)
		}
		seen[key] = struct{}{}
		canonical = append(canonical, observation)
	}
	sort.Slice(canonical, func(i, j int) bool {
		left := projectionConstraintKey(canonical[i].Metric, canonical[i].TargetID)
		right := projectionConstraintKey(canonical[j].Metric, canonical[j].TargetID)
		return left < right
	})
	telemetry.Observations = canonical
	return telemetry, nil
}

func validateProjectionObservation(constraint Constraint, observation agent.CapacityObservation, evaluatedAt time.Time) error {
	accepted := false
	for _, kind := range constraint.AcceptedEvidence {
		if observation.Evidence == kind {
			accepted = true
			break
		}
	}
	if !accepted {
		return fmt.Errorf("%w: observation evidence is not accepted by constraint %q", ErrInvalidRecommendation, constraint.ID)
	}
	if evaluatedAt.Before(observation.ObservedAt) ||
		evaluatedAt.Sub(observation.ObservedAt) > time.Duration(constraint.MaxObservationAgeSeconds)*time.Second {
		return fmt.Errorf("%w: observation for constraint %q is stale or from the future", ErrInvalidRecommendation, constraint.ID)
	}
	return nil
}

func nodeProjectionDerivationID(
	evidence NodeProjectionDerivationEvidence,
	observations []agent.CapacityObservation,
) (string, error) {
	canonical := struct {
		ProfileObjectID        string                      `json:"profile_object_id"`
		ProfileGeneration      uint64                      `json:"profile_generation"`
		ProfileResourceVersion string                      `json:"profile_resource_version"`
		TelemetrySnapshotID    string                      `json:"telemetry_snapshot_id"`
		TelemetryRevision      string                      `json:"telemetry_revision"`
		TelemetryObservedAt    string                      `json:"telemetry_observed_at"`
		EvaluatedAt            string                      `json:"evaluated_at"`
		Projection             NodeProjection              `json:"projection"`
		Observations           []agent.CapacityObservation `json:"observations"`
	}{
		evidence.ProfileObjectID,
		evidence.ProfileGeneration,
		evidence.ProfileResourceVersion,
		evidence.TelemetrySnapshotID,
		evidence.TelemetryRevision,
		evidence.TelemetryObservedAt.Format(time.RFC3339Nano),
		evidence.EvaluatedAt.Format(time.RFC3339Nano),
		evidence.Projection,
		observations,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("%w: projection derivation fingerprint: %v", ErrInvalidRecommendation, err)
	}
	digest := sha256.Sum256(encoded)
	return "npd-" + hex.EncodeToString(digest[:])[:24], nil
}

func projectionConstraintKey(metric agent.CapacityMetric, targetID string) string {
	return string(metric) + "\x00" + strings.ToLower(strings.TrimSpace(targetID))
}

func validProjectionRevision(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxProjectionRevisionLength {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}
