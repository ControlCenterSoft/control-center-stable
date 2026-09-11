package capacity

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"control-center/internal/agent"
	"control-center/internal/corecontracts"
)

// RecommendationAction is an advisory outcome. It is deliberately narrower
// than a scheduler, Change, Job, provider command, or purchase request.
type RecommendationAction string

const (
	ActionNone                 RecommendationAction = "none"
	ActionAddRoleCapacity      RecommendationAction = "add-role-capacity"
	ActionMoveRole             RecommendationAction = "move-role"
	ActionIncreaseStorage      RecommendationAction = "increase-storage"
	ActionIncreaseNetwork      RecommendationAction = "increase-network"
	ActionAdjustWorkloadPolicy RecommendationAction = "adjust-workload-policy"
	ActionReduceLoad           RecommendationAction = "reduce-load"
	ActionCollectEvidence      RecommendationAction = "collect-evidence"
)

// Bottleneck records the deterministic limiting constraint and the latest
// accepted observation used for that constraint. Negative reserve means that
// the corresponding boundary is already exceeded.
type Bottleneck struct {
	ConstraintID            string                    `json:"constraint_id"`
	Metric                  agent.CapacityMetric      `json:"metric"`
	TargetID                string                    `json:"target_id"`
	Unit                    agent.CapacityUnit        `json:"unit"`
	EvidenceID              string                    `json:"evidence_id"`
	EvidenceKind            agent.ObservationEvidence `json:"evidence_kind"`
	ObservedValue           float64                   `json:"observed_value"`
	ObservedAt              time.Time                 `json:"observed_at"`
	SafeLimit               float64                   `json:"safe_limit"`
	TechnicalLimit          float64                   `json:"technical_limit"`
	SafeReserve             float64                   `json:"safe_reserve"`
	SafeReservePercent      float64                   `json:"safe_reserve_percent"`
	TechnicalReserve        float64                   `json:"technical_reserve"`
	TechnicalReservePercent float64                   `json:"technical_reserve_percent"`
}

// Recommendation is a computed, advisory-only assessment. SafeReserve and
// TechnicalReserve use WorkloadUnit. Bottleneck reserves use Bottleneck.Unit.
// No field carries an executable command, endpoint, credential, or purchase
// instruction.
type Recommendation struct {
	corecontracts.ObjectMetadata
	SchemaVersion       string                           `json:"schema_version"`
	ProfilePrecondition corecontracts.ObjectPrecondition `json:"profile_precondition"`
	Subject             Subject                          `json:"subject"`
	EvaluatedAt         time.Time                        `json:"evaluated_at"`
	CurrentWorkload     float64                          `json:"current_workload"`
	WorkloadUnit        WorkloadUnit                     `json:"workload_unit"`
	SafeCapacity        float64                          `json:"safe_capacity"`
	TechnicalLimit      float64                          `json:"technical_limit"`
	SafeReserve         float64                          `json:"safe_reserve"`
	TechnicalReserve    float64                          `json:"technical_reserve"`
	Bottleneck          Bottleneck                       `json:"bottleneck"`
	Confidence          Confidence                       `json:"confidence"`
	Evidence            []Evidence                       `json:"evidence"`
	Action              RecommendationAction             `json:"action"`
	TargetRole          corecontracts.NodeRole           `json:"target_role,omitempty"`
	Summary             string                           `json:"summary"`
	AdvisoryOnly        bool                             `json:"advisory_only"`
}

// RecommendationDraft contains only caller-authored inputs. BuildRecommendation
// computes every capacity and bottleneck field from the guarded profile and
// current evidence.
type RecommendationDraft struct {
	corecontracts.ObjectMetadata
	ProfilePrecondition corecontracts.ObjectPrecondition
	EvaluatedAt         time.Time
	CurrentWorkload     float64
	Confidence          Confidence
	Evidence            []Evidence
	Action              RecommendationAction
	TargetRole          corecontracts.NodeRole
	Summary             string
	AdvisoryOnly        bool
}

// BuildRecommendation produces a deterministic advisory assessment. It has no
// storage, scheduling, networking, workload, or procurement side effects.
func BuildRecommendation(profile Profile, draft RecommendationDraft) (Recommendation, error) {
	profile, err := NormalizeProfile(profile)
	if err != nil {
		return Recommendation{}, fmt.Errorf("%w: profile: %v", ErrInvalidRecommendation, err)
	}
	if err := draft.ObjectMetadata.Validate(); err != nil {
		return Recommendation{}, fmt.Errorf("%w: metadata: %v", ErrInvalidRecommendation, err)
	}
	if draft.ScopeID != profile.ScopeID || draft.OwnerScope != profile.OwnerScope {
		return Recommendation{}, fmt.Errorf("%w: recommendation must keep the profile scope and owner", ErrInvalidRecommendation)
	}
	if draft.ProfilePrecondition.Generation == nil {
		return Recommendation{}, fmt.Errorf("%w: profile generation precondition is required", ErrInvalidRecommendation)
	}
	if err := draft.ProfilePrecondition.ValidateAgainst(profile.ObjectMetadata); err != nil {
		return Recommendation{}, fmt.Errorf("%w: profile precondition: %v", ErrInvalidRecommendation, err)
	}
	if draft.EvaluatedAt.IsZero() || draft.EvaluatedAt.Before(profile.CalibratedAt) || draft.EvaluatedAt.After(draft.UpdatedAt) {
		return Recommendation{}, fmt.Errorf("%w: evaluated_at must be between profile calibration and recommendation update", ErrInvalidRecommendation)
	}
	evaluatedAt := draft.EvaluatedAt.UTC()
	if !finite(draft.CurrentWorkload) || draft.CurrentWorkload < 0 {
		return Recommendation{}, fmt.Errorf("%w: current_workload must be finite and non-negative", ErrInvalidRecommendation)
	}
	evidence, err := normalizeEvidenceSet(draft.Evidence)
	if err != nil {
		return Recommendation{}, fmt.Errorf("%w: %v", ErrInvalidRecommendation, err)
	}
	if err := requireConstraintEvidence(profile.Constraints, evidence, evaluatedAt); err != nil {
		return Recommendation{}, fmt.Errorf("%w: %v", ErrInvalidRecommendation, err)
	}
	confidence, err := normalizeConfidence(draft.Confidence, evidence)
	if err != nil {
		return Recommendation{}, fmt.Errorf("%w: %v", ErrInvalidRecommendation, err)
	}
	if confidence.Score > profile.Confidence.Score+floatTolerance(confidence.Score, profile.Confidence.Score) {
		return Recommendation{}, fmt.Errorf("%w: confidence cannot exceed the guarded profile confidence", ErrInvalidRecommendation)
	}
	bottleneck, err := selectBottleneck(profile.Constraints, evidence, evaluatedAt)
	if err != nil {
		return Recommendation{}, fmt.Errorf("%w: %v", ErrInvalidRecommendation, err)
	}
	action := RecommendationAction(strings.ToLower(strings.TrimSpace(string(draft.Action))))
	targetRole := corecontracts.NodeRole(strings.ToLower(strings.TrimSpace(string(draft.TargetRole))))
	if err := validateRecommendationAction(action, targetRole, confidence, bottleneck, profile.SafeCapacity-draft.CurrentWorkload); err != nil {
		return Recommendation{}, fmt.Errorf("%w: %v", ErrInvalidRecommendation, err)
	}
	if err := validateSummary("summary", draft.Summary); err != nil {
		return Recommendation{}, fmt.Errorf("%w: %v", ErrInvalidRecommendation, err)
	}
	if !draft.AdvisoryOnly {
		return Recommendation{}, fmt.Errorf("%w: advisory_only must be true", ErrInvalidRecommendation)
	}

	return Recommendation{
		ObjectMetadata:      draft.ObjectMetadata,
		SchemaVersion:       RecommendationSchemaV1,
		ProfilePrecondition: draft.ProfilePrecondition,
		Subject:             profile.Subject,
		EvaluatedAt:         evaluatedAt,
		CurrentWorkload:     draft.CurrentWorkload,
		WorkloadUnit:        profile.WorkloadUnit,
		SafeCapacity:        profile.SafeCapacity,
		TechnicalLimit:      profile.TechnicalLimit,
		SafeReserve:         profile.SafeCapacity - draft.CurrentWorkload,
		TechnicalReserve:    profile.TechnicalLimit - draft.CurrentWorkload,
		Bottleneck:          bottleneck,
		Confidence:          confidence,
		Evidence:            evidence,
		Action:              action,
		TargetRole:          targetRole,
		Summary:             draft.Summary,
		AdvisoryOnly:        true,
	}, nil
}

// NormalizeRecommendation verifies all supplied derived fields rather than
// trusting them, then returns the deterministic canonical representation.
func NormalizeRecommendation(recommendation Recommendation, profile Profile) (Recommendation, error) {
	if strings.TrimSpace(recommendation.SchemaVersion) != RecommendationSchemaV1 {
		return Recommendation{}, fmt.Errorf("%w: recommendation %q", ErrUnsupportedSchema, recommendation.SchemaVersion)
	}
	expected, err := BuildRecommendation(profile, RecommendationDraft{
		ObjectMetadata:      recommendation.ObjectMetadata,
		ProfilePrecondition: recommendation.ProfilePrecondition,
		EvaluatedAt:         recommendation.EvaluatedAt,
		CurrentWorkload:     recommendation.CurrentWorkload,
		Confidence:          recommendation.Confidence,
		Evidence:            recommendation.Evidence,
		Action:              recommendation.Action,
		TargetRole:          recommendation.TargetRole,
		Summary:             recommendation.Summary,
		AdvisoryOnly:        recommendation.AdvisoryOnly,
	})
	if err != nil {
		return Recommendation{}, err
	}
	subject, subjectErr := normalizeSubject(recommendation.Subject)
	workloadUnit := WorkloadUnit(strings.ToLower(strings.TrimSpace(string(recommendation.WorkloadUnit))))
	if subjectErr != nil || !reflect.DeepEqual(subject, expected.Subject) || workloadUnit != expected.WorkloadUnit ||
		!closeFloat(recommendation.SafeCapacity, expected.SafeCapacity) ||
		!closeFloat(recommendation.TechnicalLimit, expected.TechnicalLimit) ||
		!closeFloat(recommendation.SafeReserve, expected.SafeReserve) ||
		!closeFloat(recommendation.TechnicalReserve, expected.TechnicalReserve) ||
		!equalBottleneck(recommendation.Bottleneck, expected.Bottleneck) {
		return Recommendation{}, fmt.Errorf("%w: supplied derived fields do not match the guarded profile and evidence", ErrInvalidRecommendation)
	}
	return expected, nil
}

// ValidateRecommendationSuccessor applies the canonical object-version rules
// after validating both recommendations against the same current profile.
func ValidateRecommendationSuccessor(current, next Recommendation, profile Profile) error {
	current, err := NormalizeRecommendation(current, profile)
	if err != nil {
		return err
	}
	next, err = NormalizeRecommendation(next, profile)
	if err != nil {
		return err
	}
	desiredChanged := !reflect.DeepEqual(recommendationSpec(current), recommendationSpec(next))
	return corecontracts.ValidateSuccessor(current.ObjectMetadata, next.ObjectMetadata, desiredChanged)
}

type normalizedRecommendationSpec struct {
	ProfilePrecondition corecontracts.ObjectPrecondition
	Subject             Subject
	EvaluatedAt         time.Time
	CurrentWorkload     float64
	WorkloadUnit        WorkloadUnit
	SafeCapacity        float64
	TechnicalLimit      float64
	SafeReserve         float64
	TechnicalReserve    float64
	Bottleneck          Bottleneck
	Confidence          Confidence
	Evidence            []Evidence
	Action              RecommendationAction
	TargetRole          corecontracts.NodeRole
	Summary             string
	AdvisoryOnly        bool
}

func recommendationSpec(value Recommendation) normalizedRecommendationSpec {
	return normalizedRecommendationSpec{
		ProfilePrecondition: value.ProfilePrecondition, Subject: value.Subject, EvaluatedAt: value.EvaluatedAt,
		CurrentWorkload: value.CurrentWorkload, WorkloadUnit: value.WorkloadUnit,
		SafeCapacity: value.SafeCapacity, TechnicalLimit: value.TechnicalLimit,
		SafeReserve: value.SafeReserve, TechnicalReserve: value.TechnicalReserve,
		Bottleneck: value.Bottleneck, Confidence: value.Confidence, Evidence: value.Evidence,
		Action: value.Action, TargetRole: value.TargetRole, Summary: value.Summary, AdvisoryOnly: value.AdvisoryOnly,
	}
}

func selectBottleneck(constraints []Constraint, evidence []Evidence, evaluatedAt time.Time) (Bottleneck, error) {
	ordered := append([]Constraint(nil), constraints...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	var selected Bottleneck
	selectedSet := false
	for _, constraint := range ordered {
		accepted := make(map[agent.ObservationEvidence]struct{}, len(constraint.AcceptedEvidence))
		for _, kind := range constraint.AcceptedEvidence {
			accepted[kind] = struct{}{}
		}
		var latest Evidence
		latestSet := false
		for _, item := range evidence {
			observation := item.Observation
			if observation.Metric != constraint.Metric || !strings.EqualFold(observation.TargetID, constraint.TargetID) || observation.Unit != constraint.Unit {
				continue
			}
			if _, allowed := accepted[observation.Evidence]; !allowed || observation.ObservedAt.After(evaluatedAt) ||
				evaluatedAt.Sub(observation.ObservedAt) > time.Duration(constraint.MaxObservationAgeSeconds)*time.Second {
				continue
			}
			if !latestSet || observation.ObservedAt.After(latest.Observation.ObservedAt) ||
				(observation.ObservedAt.Equal(latest.Observation.ObservedAt) && item.ID < latest.ID) {
				latest = item
				latestSet = true
			}
		}
		if !latestSet {
			return Bottleneck{}, fmt.Errorf("constraint %q has no current accepted observation", constraint.ID)
		}
		candidate := Bottleneck{
			ConstraintID:            constraint.ID,
			Metric:                  constraint.Metric,
			TargetID:                constraint.TargetID,
			Unit:                    constraint.Unit,
			EvidenceID:              latest.ID,
			EvidenceKind:            latest.Observation.Evidence,
			ObservedValue:           latest.Observation.Value,
			ObservedAt:              latest.Observation.ObservedAt,
			SafeLimit:               constraint.SafeLimit,
			TechnicalLimit:          constraint.TechnicalLimit,
			SafeReserve:             constraint.SafeLimit - latest.Observation.Value,
			SafeReservePercent:      (constraint.SafeLimit - latest.Observation.Value) / constraint.SafeLimit * 100,
			TechnicalReserve:        constraint.TechnicalLimit - latest.Observation.Value,
			TechnicalReservePercent: (constraint.TechnicalLimit - latest.Observation.Value) / constraint.TechnicalLimit * 100,
		}
		if !selectedSet || candidate.SafeReservePercent < selected.SafeReservePercent-floatTolerance(candidate.SafeReservePercent, selected.SafeReservePercent) {
			selected = candidate
			selectedSet = true
		}
	}
	if !selectedSet {
		return Bottleneck{}, fmt.Errorf("%w: no constraint is available", ErrInvalidConstraint)
	}
	return selected, nil
}

func validateRecommendationAction(action RecommendationAction, targetRole corecontracts.NodeRole, confidence Confidence, bottleneck Bottleneck, workloadSafeReserve float64) error {
	valid := false
	switch action {
	case ActionNone, ActionAddRoleCapacity, ActionMoveRole, ActionIncreaseStorage, ActionIncreaseNetwork,
		ActionAdjustWorkloadPolicy, ActionReduceLoad, ActionCollectEvidence:
		valid = true
	}
	if !valid {
		return fmt.Errorf("unsupported action %q", action)
	}
	roleAction := action == ActionAddRoleCapacity || action == ActionMoveRole
	if roleAction {
		if !targetRole.Valid() {
			return fmt.Errorf("action %q requires a canonical target_role", action)
		}
	} else if targetRole != "" {
		return fmt.Errorf("target_role is only valid for role capacity actions")
	}
	definition, exists := agent.DefinitionForCapacityMetric(bottleneck.Metric)
	if !exists {
		return fmt.Errorf("bottleneck metric %q is unknown", bottleneck.Metric)
	}
	if action == ActionIncreaseStorage && definition.TargetKind != agent.CapacityTargetStorage {
		return fmt.Errorf("increase-storage requires a storage bottleneck")
	}
	if action == ActionIncreaseNetwork && definition.TargetKind != agent.CapacityTargetNetwork {
		return fmt.Errorf("increase-network requires a network bottleneck")
	}
	if confidence.Level == ConfidenceLow && action != ActionCollectEvidence {
		return fmt.Errorf("low confidence only permits collect-evidence")
	}
	safeBreached := workloadSafeReserve < -floatTolerance(workloadSafeReserve) || bottleneck.SafeReserve < -floatTolerance(bottleneck.SafeReserve)
	if safeBreached && action == ActionNone {
		return fmt.Errorf("safe boundary breach requires an advisory action")
	}
	if !safeBreached && action != ActionNone && action != ActionCollectEvidence {
		return fmt.Errorf("capacity action requires a safe boundary breach")
	}
	return nil
}

func equalBottleneck(left, right Bottleneck) bool {
	return left.ConstraintID == right.ConstraintID && left.Metric == right.Metric &&
		strings.EqualFold(left.TargetID, right.TargetID) && left.Unit == right.Unit &&
		left.EvidenceID == right.EvidenceID && left.EvidenceKind == right.EvidenceKind &&
		left.ObservedAt.Equal(right.ObservedAt) && closeFloat(left.ObservedValue, right.ObservedValue) &&
		closeFloat(left.SafeLimit, right.SafeLimit) && closeFloat(left.TechnicalLimit, right.TechnicalLimit) &&
		closeFloat(left.SafeReserve, right.SafeReserve) && closeFloat(left.SafeReservePercent, right.SafeReservePercent) &&
		closeFloat(left.TechnicalReserve, right.TechnicalReserve) && closeFloat(left.TechnicalReservePercent, right.TechnicalReservePercent)
}
