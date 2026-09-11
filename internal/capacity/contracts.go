// Package capacity defines versioned, side-effect-free Capacity Planner
// contracts. It validates evidence and recommendations but never schedules,
// places, purchases, or mutates infrastructure.
package capacity

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"control-center/internal/agent"
	"control-center/internal/corecontracts"
)

const (
	ProfileSchemaV1        = "capacity.profile/v1"
	ConstraintSchemaV1     = "capacity.constraint/v1"
	RecommendationSchemaV1 = "capacity.recommendation/v1"

	maxConstraints = 64
	maxEvidence    = 128
	maxIdentifier  = 255
	maxTargetID    = 128
	maxSummary     = 1024
)

var (
	ErrInvalidProfile        = errors.New("invalid capacity profile")
	ErrInvalidConstraint     = errors.New("invalid capacity constraint")
	ErrInvalidEvidence       = errors.New("invalid capacity evidence")
	ErrInvalidConfidence     = errors.New("invalid capacity confidence")
	ErrInvalidRecommendation = errors.New("invalid capacity recommendation")
	ErrUnsupportedSchema     = errors.New("unsupported capacity schema")

	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._:-]{0,253}[A-Za-z0-9])?$`)
)

type SubjectKind string

const (
	SubjectNode         SubjectKind = "node"
	SubjectRole         SubjectKind = "role"
	SubjectService      SubjectKind = "service"
	SubjectMarketModule SubjectKind = "market-module"
	SubjectSite         SubjectKind = "site"
)

type WorkloadUnit string

const (
	WorkloadDevices            WorkloadUnit = "devices"
	WorkloadConcurrentSessions WorkloadUnit = "concurrent-sessions"
	WorkloadJobsPerMinute      WorkloadUnit = "jobs-per-minute"
	WorkloadRequestsPerSecond  WorkloadUnit = "requests-per-second"
	WorkloadInstances          WorkloadUnit = "instances"
)

type Subject struct {
	Kind SubjectKind            `json:"kind"`
	ID   string                 `json:"id"`
	Role corecontracts.NodeRole `json:"role,omitempty"`
}

// Evidence embeds the canonical Agent observation instead of copying its
// metric, unit, target, or evidence enums into another capacity dialect.
type Evidence struct {
	ID          string                    `json:"id"`
	Observation agent.CapacityObservation `json:"observation"`
	SampleCount uint64                    `json:"sample_count"`
}

type ConfidenceLevel string

const (
	ConfidenceLow       ConfidenceLevel = "low"
	ConfidenceMedium    ConfidenceLevel = "medium"
	ConfidenceHigh      ConfidenceLevel = "high"
	ConfidenceCertified ConfidenceLevel = "certified"
)

type Confidence struct {
	Level ConfidenceLevel `json:"level"`
	Score float64         `json:"score"`
}

// Constraint describes an upper resource-pressure boundary. SafeLimit is the
// operational boundary; TechnicalLimit is a low-confidence saturation bound.
type Constraint struct {
	SchemaVersion            string                      `json:"schema_version"`
	ID                       string                      `json:"id"`
	Metric                   agent.CapacityMetric        `json:"metric"`
	TargetID                 string                      `json:"target_id"`
	Unit                     agent.CapacityUnit          `json:"unit"`
	SafeLimit                float64                     `json:"safe_limit"`
	TechnicalLimit           float64                     `json:"technical_limit"`
	MaxObservationAgeSeconds uint32                      `json:"max_observation_age_seconds"`
	MinimumSamples           uint64                      `json:"minimum_samples"`
	AcceptedEvidence         []agent.ObservationEvidence `json:"accepted_evidence"`
}

// Profile is a portable, evidence-backed capacity baseline for one logical
// subject and workload unit.
type Profile struct {
	corecontracts.ObjectMetadata
	SchemaVersion         string       `json:"schema_version"`
	Subject               Subject      `json:"subject"`
	WorkloadUnit          WorkloadUnit `json:"workload_unit"`
	SafeCapacity          float64      `json:"safe_capacity"`
	TechnicalLimit        float64      `json:"technical_limit"`
	MinimumReservePercent float64      `json:"minimum_reserve_percent"`
	Constraints           []Constraint `json:"constraints"`
	Confidence            Confidence   `json:"confidence"`
	Evidence              []Evidence   `json:"evidence"`
	CalibratedAt          time.Time    `json:"calibrated_at"`
}

// NormalizeProfile returns a canonical defensive copy and rejects missing or
// unknown schema versions. There is no implicit legacy migration because no
// pre-v1 persisted CapacityProfile contract exists.
func NormalizeProfile(profile Profile) (Profile, error) {
	if strings.TrimSpace(profile.SchemaVersion) != ProfileSchemaV1 {
		return Profile{}, fmt.Errorf("%w: profile %q", ErrUnsupportedSchema, profile.SchemaVersion)
	}
	profile.SchemaVersion = ProfileSchemaV1
	if err := profile.ObjectMetadata.Validate(); err != nil {
		return Profile{}, fmt.Errorf("%w: %v", ErrInvalidProfile, err)
	}
	subject, err := normalizeSubject(profile.Subject)
	if err != nil {
		return Profile{}, fmt.Errorf("%w: %v", ErrInvalidProfile, err)
	}
	profile.Subject = subject
	profile.WorkloadUnit = WorkloadUnit(strings.ToLower(strings.TrimSpace(string(profile.WorkloadUnit))))
	if !validWorkloadUnit(profile.WorkloadUnit) {
		return Profile{}, fmt.Errorf("%w: unsupported workload_unit %q", ErrInvalidProfile, profile.WorkloadUnit)
	}
	if !finitePositive(profile.SafeCapacity) || !finitePositive(profile.TechnicalLimit) || profile.SafeCapacity >= profile.TechnicalLimit {
		return Profile{}, fmt.Errorf("%w: safe_capacity must be positive and below technical_limit", ErrInvalidProfile)
	}
	if !finite(profile.MinimumReservePercent) || profile.MinimumReservePercent <= 0 || profile.MinimumReservePercent > 90 {
		return Profile{}, fmt.Errorf("%w: minimum_reserve_percent must be in (0,90]", ErrInvalidProfile)
	}
	actualReservePercent := (profile.TechnicalLimit - profile.SafeCapacity) / profile.TechnicalLimit * 100
	if actualReservePercent+floatTolerance(actualReservePercent, profile.MinimumReservePercent) < profile.MinimumReservePercent {
		return Profile{}, fmt.Errorf("%w: safe_capacity does not preserve minimum reserve", ErrInvalidProfile)
	}
	if profile.CalibratedAt.IsZero() || profile.CalibratedAt.After(profile.UpdatedAt) {
		return Profile{}, fmt.Errorf("%w: calibrated_at is required and must not follow updated_at", ErrInvalidProfile)
	}
	profile.CalibratedAt = profile.CalibratedAt.UTC()

	if len(profile.Constraints) == 0 || len(profile.Constraints) > maxConstraints {
		return Profile{}, fmt.Errorf("%w: constraints must contain between 1 and %d items", ErrInvalidProfile, maxConstraints)
	}
	constraints := make([]Constraint, 0, len(profile.Constraints))
	constraintIDs := make(map[string]struct{}, len(profile.Constraints))
	constraintTargets := make(map[string]struct{}, len(profile.Constraints))
	for _, candidate := range profile.Constraints {
		constraint, err := NormalizeConstraint(candidate)
		if err != nil {
			return Profile{}, fmt.Errorf("%w: %v", ErrInvalidProfile, err)
		}
		if _, duplicate := constraintIDs[constraint.ID]; duplicate {
			return Profile{}, fmt.Errorf("%w: duplicate constraint id %q", ErrInvalidProfile, constraint.ID)
		}
		constraintIDs[constraint.ID] = struct{}{}
		targetKey := string(constraint.Metric) + "\x00" + strings.ToLower(constraint.TargetID)
		if _, duplicate := constraintTargets[targetKey]; duplicate {
			return Profile{}, fmt.Errorf("%w: duplicate metric/target constraint", ErrInvalidProfile)
		}
		constraintTargets[targetKey] = struct{}{}
		constraints = append(constraints, constraint)
	}
	sort.Slice(constraints, func(i, j int) bool { return constraints[i].ID < constraints[j].ID })
	profile.Constraints = constraints

	evidence, err := normalizeEvidenceSet(profile.Evidence)
	if err != nil {
		return Profile{}, fmt.Errorf("%w: %v", ErrInvalidProfile, err)
	}
	for _, item := range evidence {
		if item.Observation.ObservedAt.After(profile.CalibratedAt) {
			return Profile{}, fmt.Errorf("%w: evidence %q follows calibrated_at", ErrInvalidProfile, item.ID)
		}
	}
	if err := requireConstraintEvidence(profile.Constraints, evidence, profile.CalibratedAt); err != nil {
		return Profile{}, fmt.Errorf("%w: %v", ErrInvalidProfile, err)
	}
	profile.Evidence = evidence
	confidence, err := normalizeConfidence(profile.Confidence, evidence)
	if err != nil {
		return Profile{}, fmt.Errorf("%w: %v", ErrInvalidProfile, err)
	}
	profile.Confidence = confidence
	return profile, nil
}

// NormalizeConstraint canonicalizes a standalone constraint and verifies it
// against the Agent metric registry.
func NormalizeConstraint(constraint Constraint) (Constraint, error) {
	if strings.TrimSpace(constraint.SchemaVersion) != ConstraintSchemaV1 {
		return Constraint{}, fmt.Errorf("%w: constraint %q", ErrUnsupportedSchema, constraint.SchemaVersion)
	}
	constraint.SchemaVersion = ConstraintSchemaV1
	constraint.ID = strings.TrimSpace(constraint.ID)
	constraint.TargetID = strings.TrimSpace(constraint.TargetID)
	constraint.Metric = agent.CapacityMetric(strings.ToLower(strings.TrimSpace(string(constraint.Metric))))
	constraint.Unit = agent.CapacityUnit(strings.ToLower(strings.TrimSpace(string(constraint.Unit))))
	if err := validateIdentifier("constraint.id", constraint.ID); err != nil {
		return Constraint{}, fmt.Errorf("%w: %v", ErrInvalidConstraint, err)
	}
	if err := validateBoundedIdentifier("constraint.target_id", constraint.TargetID, maxTargetID); err != nil {
		return Constraint{}, fmt.Errorf("%w: %v", ErrInvalidConstraint, err)
	}
	definition, exists := agent.DefinitionForCapacityMetric(constraint.Metric)
	if !exists || definition.Unit != constraint.Unit {
		return Constraint{}, fmt.Errorf("%w: metric %q does not use unit %q", ErrInvalidConstraint, constraint.Metric, constraint.Unit)
	}
	if !agent.ValidCapacityMetricValue(constraint.Metric, constraint.SafeLimit) ||
		!agent.ValidCapacityMetricValue(constraint.Metric, constraint.TechnicalLimit) ||
		constraint.SafeLimit <= 0 || constraint.SafeLimit >= constraint.TechnicalLimit {
		return Constraint{}, fmt.Errorf("%w: safe_limit must be positive and below technical_limit", ErrInvalidConstraint)
	}
	if constraint.MaxObservationAgeSeconds == 0 || constraint.MaxObservationAgeSeconds > 7*24*60*60 {
		return Constraint{}, fmt.Errorf("%w: max_observation_age_seconds must be in 1..604800", ErrInvalidConstraint)
	}
	if constraint.MinimumSamples == 0 || constraint.MinimumSamples > 1_000_000_000_000 {
		return Constraint{}, fmt.Errorf("%w: minimum_samples must be in 1..1000000000000", ErrInvalidConstraint)
	}
	if len(constraint.AcceptedEvidence) == 0 || len(constraint.AcceptedEvidence) > 3 {
		return Constraint{}, fmt.Errorf("%w: accepted_evidence must contain 1..3 values", ErrInvalidConstraint)
	}
	accepted := make([]agent.ObservationEvidence, 0, len(constraint.AcceptedEvidence))
	seen := make(map[agent.ObservationEvidence]struct{}, len(constraint.AcceptedEvidence))
	for _, value := range constraint.AcceptedEvidence {
		value = agent.ObservationEvidence(strings.ToLower(strings.TrimSpace(string(value))))
		if !validEvidenceKind(value) {
			return Constraint{}, fmt.Errorf("%w: unsupported evidence %q", ErrInvalidConstraint, value)
		}
		if _, duplicate := seen[value]; duplicate {
			return Constraint{}, fmt.Errorf("%w: duplicate accepted evidence %q", ErrInvalidConstraint, value)
		}
		seen[value] = struct{}{}
		accepted = append(accepted, value)
	}
	sort.Slice(accepted, func(i, j int) bool { return accepted[i] < accepted[j] })
	constraint.AcceptedEvidence = accepted
	return constraint, nil
}

// ValidateProfileSuccessor applies canonical generation/resource-version rules
// without storing either profile.
func ValidateProfileSuccessor(current, next Profile) error {
	currentNormalized, err := NormalizeProfile(current)
	if err != nil {
		return err
	}
	nextNormalized, err := NormalizeProfile(next)
	if err != nil {
		return err
	}
	desiredChanged := !reflect.DeepEqual(profileSpec(currentNormalized), profileSpec(nextNormalized))
	return corecontracts.ValidateSuccessor(currentNormalized.ObjectMetadata, nextNormalized.ObjectMetadata, desiredChanged)
}

type normalizedProfileSpec struct {
	SchemaVersion         string
	Subject               Subject
	WorkloadUnit          WorkloadUnit
	SafeCapacity          float64
	TechnicalLimit        float64
	MinimumReservePercent float64
	Constraints           []Constraint
	Confidence            Confidence
	Evidence              []Evidence
	CalibratedAt          time.Time
}

func profileSpec(profile Profile) normalizedProfileSpec {
	return normalizedProfileSpec{
		SchemaVersion: profile.SchemaVersion, Subject: profile.Subject, WorkloadUnit: profile.WorkloadUnit,
		SafeCapacity: profile.SafeCapacity, TechnicalLimit: profile.TechnicalLimit,
		MinimumReservePercent: profile.MinimumReservePercent, Constraints: profile.Constraints,
		Confidence: profile.Confidence, Evidence: profile.Evidence, CalibratedAt: profile.CalibratedAt,
	}
}

func normalizeSubject(subject Subject) (Subject, error) {
	subject.Kind = SubjectKind(strings.ToLower(strings.TrimSpace(string(subject.Kind))))
	subject.ID = strings.TrimSpace(subject.ID)
	if err := validateIdentifier("subject.id", subject.ID); err != nil {
		return Subject{}, err
	}
	switch subject.Kind {
	case SubjectNode, SubjectService, SubjectMarketModule, SubjectSite:
		if subject.Role != "" {
			return Subject{}, errors.New("subject.role is only valid for a role subject")
		}
	case SubjectRole:
		subject.Role = corecontracts.NodeRole(strings.ToLower(strings.TrimSpace(string(subject.Role))))
		if !subject.Role.Valid() {
			return Subject{}, errors.New("role subject requires a canonical Core role")
		}
	default:
		return Subject{}, fmt.Errorf("unsupported subject kind %q", subject.Kind)
	}
	return subject, nil
}

func normalizeEvidenceSet(values []Evidence) ([]Evidence, error) {
	if len(values) == 0 || len(values) > maxEvidence {
		return nil, fmt.Errorf("%w: evidence must contain between 1 and %d items", ErrInvalidEvidence, maxEvidence)
	}
	result := make([]Evidence, 0, len(values))
	ids := make(map[string]struct{}, len(values))
	signatures := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.ID = strings.TrimSpace(value.ID)
		if err := validateIdentifier("evidence.id", value.ID); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidEvidence, err)
		}
		if _, duplicate := ids[value.ID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate id %q", ErrInvalidEvidence, value.ID)
		}
		ids[value.ID] = struct{}{}
		if value.SampleCount == 0 || value.SampleCount > 1_000_000_000_000 {
			return nil, fmt.Errorf("%w: evidence %q sample_count is outside 1..1000000000000", ErrInvalidEvidence, value.ID)
		}
		observation, err := normalizeObservation(value.Observation)
		if err != nil {
			return nil, fmt.Errorf("%w: evidence %q: %v", ErrInvalidEvidence, value.ID, err)
		}
		value.Observation = observation
		signature := string(observation.Metric) + "\x00" + strings.ToLower(observation.TargetID) + "\x00" + observation.ObservedAt.Format(time.RFC3339Nano) + "\x00" + string(observation.Evidence)
		if _, duplicate := signatures[signature]; duplicate {
			return nil, fmt.Errorf("%w: duplicate observation signature", ErrInvalidEvidence)
		}
		signatures[signature] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func normalizeObservation(observation agent.CapacityObservation) (agent.CapacityObservation, error) {
	observation.Metric = agent.CapacityMetric(strings.ToLower(strings.TrimSpace(string(observation.Metric))))
	observation.TargetID = strings.TrimSpace(observation.TargetID)
	observation.Unit = agent.CapacityUnit(strings.ToLower(strings.TrimSpace(string(observation.Unit))))
	observation.Evidence = agent.ObservationEvidence(strings.ToLower(strings.TrimSpace(string(observation.Evidence))))
	definition, exists := agent.DefinitionForCapacityMetric(observation.Metric)
	if !exists || definition.Unit != observation.Unit {
		return agent.CapacityObservation{}, fmt.Errorf("metric %q does not use unit %q", observation.Metric, observation.Unit)
	}
	if err := validateBoundedIdentifier("observation.target_id", observation.TargetID, maxTargetID); err != nil {
		return agent.CapacityObservation{}, err
	}
	if !agent.ValidCapacityMetricValue(observation.Metric, observation.Value) {
		return agent.CapacityObservation{}, errors.New("observation value is outside canonical metric bounds")
	}
	if observation.ObservedAt.IsZero() {
		return agent.CapacityObservation{}, errors.New("observation.observed_at is required")
	}
	if !validEvidenceKind(observation.Evidence) {
		return agent.CapacityObservation{}, fmt.Errorf("unsupported observation evidence %q", observation.Evidence)
	}
	observation.ObservedAt = observation.ObservedAt.UTC()
	return observation, nil
}

func normalizeConfidence(confidence Confidence, evidence []Evidence) (Confidence, error) {
	confidence.Level = ConfidenceLevel(strings.ToLower(strings.TrimSpace(string(confidence.Level))))
	if !finite(confidence.Score) || confidence.Score < 0 || confidence.Score > 1 {
		return Confidence{}, fmt.Errorf("%w: score must be in [0,1]", ErrInvalidConfidence)
	}
	validBand := false
	switch confidence.Level {
	case ConfidenceLow:
		validBand = confidence.Score < 0.5
	case ConfidenceMedium:
		validBand = confidence.Score >= 0.5 && confidence.Score < 0.75
	case ConfidenceHigh:
		validBand = confidence.Score >= 0.75 && confidence.Score < 0.95
	case ConfidenceCertified:
		validBand = confidence.Score >= 0.95
	default:
		return Confidence{}, fmt.Errorf("%w: unsupported level %q", ErrInvalidConfidence, confidence.Level)
	}
	if !validBand {
		return Confidence{}, fmt.Errorf("%w: score does not match level %q", ErrInvalidConfidence, confidence.Level)
	}
	hasMeasured := false
	hasBenchmark := false
	hasNonEstimated := false
	var samples uint64
	for _, item := range evidence {
		switch item.Observation.Evidence {
		case agent.EvidenceMeasured:
			hasMeasured = true
			hasNonEstimated = true
		case agent.EvidenceBenchmark:
			hasBenchmark = true
			hasNonEstimated = true
		}
		if math.MaxUint64-samples < item.SampleCount {
			return Confidence{}, fmt.Errorf("%w: sample count overflow", ErrInvalidConfidence)
		}
		samples += item.SampleCount
	}
	if confidence.Level == ConfidenceCertified && (!hasMeasured || !hasBenchmark || samples < 100) {
		return Confidence{}, fmt.Errorf("%w: certified requires measured and benchmark evidence with at least 100 samples", ErrInvalidConfidence)
	}
	if (confidence.Level == ConfidenceMedium || confidence.Level == ConfidenceHigh) && !hasNonEstimated {
		return Confidence{}, fmt.Errorf("%w: medium/high confidence requires non-estimated evidence", ErrInvalidConfidence)
	}
	return confidence, nil
}

func requireConstraintEvidence(constraints []Constraint, evidence []Evidence, evaluatedAt time.Time) error {
	for _, constraint := range constraints {
		accepted := make(map[agent.ObservationEvidence]struct{}, len(constraint.AcceptedEvidence))
		for _, kind := range constraint.AcceptedEvidence {
			accepted[kind] = struct{}{}
		}
		var samples uint64
		matched := false
		for _, item := range evidence {
			observation := item.Observation
			if observation.Metric != constraint.Metric || !strings.EqualFold(observation.TargetID, constraint.TargetID) || observation.Unit != constraint.Unit {
				continue
			}
			if _, allowed := accepted[observation.Evidence]; !allowed {
				continue
			}
			if !evaluatedAt.IsZero() {
				if observation.ObservedAt.After(evaluatedAt) || evaluatedAt.Sub(observation.ObservedAt) > time.Duration(constraint.MaxObservationAgeSeconds)*time.Second {
					continue
				}
			}
			matched = true
			if math.MaxUint64-samples < item.SampleCount {
				return fmt.Errorf("%w: sample count overflow", ErrInvalidEvidence)
			}
			samples += item.SampleCount
		}
		if !matched || samples < constraint.MinimumSamples {
			return fmt.Errorf("%w: constraint %q lacks accepted evidence/sample coverage", ErrInvalidEvidence, constraint.ID)
		}
	}
	return nil
}

func validEvidenceKind(value agent.ObservationEvidence) bool {
	return value == agent.EvidenceMeasured || value == agent.EvidenceEstimated || value == agent.EvidenceBenchmark
}

func validWorkloadUnit(value WorkloadUnit) bool {
	switch value {
	case WorkloadDevices, WorkloadConcurrentSessions, WorkloadJobsPerMinute, WorkloadRequestsPerSecond, WorkloadInstances:
		return true
	default:
		return false
	}
}

func validateIdentifier(field, value string) error {
	return validateBoundedIdentifier(field, value, maxIdentifier)
}

func validateBoundedIdentifier(field, value string, maximum int) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > maximum || !identifierPattern.MatchString(value) {
		return fmt.Errorf("%s must be a bounded canonical identifier", field)
	}
	return nil
}

func validateSummary(field, value string) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > maxSummary {
		return fmt.Errorf("%s must contain 1..%d bytes without surrounding whitespace", field, maxSummary)
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\n' && character != '\t' {
			return fmt.Errorf("%s contains an unsupported control character", field)
		}
	}
	return nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && math.Abs(value) <= 1e21
}

func finitePositive(value float64) bool { return finite(value) && value > 0 }

func floatTolerance(values ...float64) float64 {
	scale := 1.0
	for _, value := range values {
		if absolute := math.Abs(value); absolute > scale {
			scale = absolute
		}
	}
	return scale * 1e-9
}

func closeFloat(left, right float64) bool {
	return finite(left) && finite(right) && math.Abs(left-right) <= floatTolerance(left, right)
}
