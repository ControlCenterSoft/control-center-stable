package operationsview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"control-center/internal/orchestration/change"
	"control-center/internal/orchestration/events"
	"control-center/internal/orchestration/job"
)

const (
	JobResultEvidenceContractVersion     = "ui.operations-job-result-evidence/v1"
	MaxJobResultEvidenceRecords          = 512
	MaxJobResultEvidenceIdentifierLength = 255
)

var ErrInvalidJobResultEvidence = errors.New("invalid operations job result evidence")

// JobResultEvidence is a bounded, read-only projection of one terminal Job and
// the exact output persisted with it. Raw output details, health messages, audit
// details, job input, errors, idempotency keys and lease material are never
// projected. OutputDigest still binds the projection to the complete persisted
// output, including fields intentionally hidden from the operator summary.
type JobResultEvidence struct {
	ContractVersion string              `json:"contract_version"`
	ChangeID        string              `json:"change_id"`
	RevisionID      string              `json:"revision_id"`
	RevisionDigest  string              `json:"revision_digest"`
	JobID           string              `json:"job_id"`
	ActionName      string              `json:"action_name"`
	JobVersion      uint64              `json:"job_version"`
	Outcome         job.Status          `json:"outcome"`
	OutputPresent   bool                `json:"output_present"`
	OutputDigest    string              `json:"output_digest,omitempty"`
	ActualStates    int                 `json:"actual_states"`
	HealthChecks    int                 `json:"health_checks"`
	AuditEvents     int                 `json:"audit_events"`
	WorstHealth     events.HealthStatus `json:"worst_health,omitempty"`
	ObservedAt      time.Time           `json:"observed_at"`
}

type JobResultEvidenceInput struct {
	Change         change.Snapshot
	Job            job.Job
	RevisionDigest string
	ObservedAt     time.Time
}

// BuildJobResultEvidence binds terminal execution evidence to the exact Change
// revision and durable Job version. It is deliberately non-authorizing: the
// returned digest can prove what result was persisted, but cannot approve,
// retry, cancel, execute or mutate anything.
func BuildJobResultEvidence(input JobResultEvidenceInput) (JobResultEvidence, error) {
	snapshot := input.Change
	source := input.Job

	if err := validateResultIdentity("change id", snapshot.ID); err != nil {
		return JobResultEvidence{}, err
	}
	if err := validateResultIdentity("revision id", snapshot.RevisionID); err != nil {
		return JobResultEvidence{}, err
	}
	if err := validateResultIdentity("change action", snapshot.Action); err != nil {
		return JobResultEvidence{}, err
	}
	if !validResultDigest(input.RevisionDigest) {
		return JobResultEvidence{}, fmt.Errorf("%w: canonical revision digest is required", ErrInvalidJobResultEvidence)
	}
	if err := validateResultIdentity("job id", source.ID); err != nil {
		return JobResultEvidence{}, err
	}
	if err := validateResultIdentity("job change id", source.ChangeID); err != nil {
		return JobResultEvidence{}, err
	}
	if err := validateResultIdentity("job action", source.ActionName); err != nil {
		return JobResultEvidence{}, err
	}
	if source.ChangeID != snapshot.ID || source.ActionName != snapshot.Action {
		return JobResultEvidence{}, fmt.Errorf("%w: Job is not bound to the supplied Change", ErrInvalidJobResultEvidence)
	}
	if snapshot.Version == 0 || snapshot.UpdatedAt.IsZero() {
		return JobResultEvidence{}, fmt.Errorf("%w: complete Change state is required", ErrInvalidJobResultEvidence)
	}
	if source.Version == 0 || source.CreatedAt.IsZero() || source.UpdatedAt.IsZero() || source.UpdatedAt.Before(source.CreatedAt) {
		return JobResultEvidence{}, fmt.Errorf("%w: complete Job state is required", ErrInvalidJobResultEvidence)
	}
	if source.MaxAttempts <= 0 || source.Attempt < 0 || source.Attempt > source.MaxAttempts {
		return JobResultEvidence{}, fmt.Errorf("%w: invalid Job attempt state", ErrInvalidJobResultEvidence)
	}
	if input.ObservedAt.IsZero() {
		return JobResultEvidence{}, fmt.Errorf("%w: observed time is required", ErrInvalidJobResultEvidence)
	}
	observedAt := input.ObservedAt.UTC()
	if snapshot.UpdatedAt.After(observedAt) || source.UpdatedAt.After(observedAt) {
		return JobResultEvidence{}, fmt.Errorf("%w: source state is newer than observed evidence", ErrInvalidJobResultEvidence)
	}
	if err := validateTerminalResultState(snapshot.State, source.Status); err != nil {
		return JobResultEvidence{}, err
	}

	evidence := JobResultEvidence{
		ContractVersion: JobResultEvidenceContractVersion,
		ChangeID:        snapshot.ID,
		RevisionID:      snapshot.RevisionID,
		RevisionDigest:  input.RevisionDigest,
		JobID:           source.ID,
		ActionName:      source.ActionName,
		JobVersion:      source.Version,
		Outcome:         source.Status,
		ObservedAt:      observedAt,
	}
	if source.Output == nil {
		if source.Status != job.StatusCancelled {
			return JobResultEvidence{}, fmt.Errorf("%w: terminal execution result is missing output evidence", ErrInvalidJobResultEvidence)
		}
		return evidence, nil
	}
	if err := validateResultOutput(*source.Output, source.CreatedAt.UTC(), source.UpdatedAt.UTC(), observedAt); err != nil {
		return JobResultEvidence{}, err
	}

	digest, err := digestResultOutput(*source.Output)
	if err != nil {
		return JobResultEvidence{}, err
	}
	evidence.OutputPresent = true
	evidence.OutputDigest = digest
	evidence.ActualStates = len(source.Output.ActualStates)
	evidence.HealthChecks = len(source.Output.Health)
	evidence.AuditEvents = len(source.Output.AuditEvents)
	for _, health := range source.Output.Health {
		evidence.WorstHealth = worseResultHealth(evidence.WorstHealth, health.Status)
	}
	return evidence, nil
}

func validateTerminalResultState(changeState change.State, status job.Status) error {
	var expected change.State
	switch status {
	case job.StatusSucceeded:
		expected = change.StateSucceeded
	case job.StatusFailed:
		expected = change.StateFailed
	case job.StatusCancelled:
		expected = change.StateCancelled
	default:
		return fmt.Errorf("%w: Job is not terminal", ErrInvalidJobResultEvidence)
	}
	if changeState != expected {
		return fmt.Errorf("%w: Change state %q does not match terminal Job outcome %q", ErrInvalidJobResultEvidence, changeState, status)
	}
	return nil
}

func validateResultOutput(output events.Output, startedAt, completedAt, observedAt time.Time) error {
	if len(output.ActualStates) > MaxJobResultEvidenceRecords || len(output.Health) > MaxJobResultEvidenceRecords || len(output.AuditEvents) > MaxJobResultEvidenceRecords {
		return fmt.Errorf("%w: output exceeds bounded review surface", ErrInvalidJobResultEvidence)
	}
	for index, state := range output.ActualStates {
		if err := validateResultIdentity("actual-state resource id", state.ResourceID); err != nil {
			return fmt.Errorf("%w: actual state %d: %v", ErrInvalidJobResultEvidence, index, err)
		}
		if err := validateResultIdentity("actual-state kind", state.Kind); err != nil {
			return fmt.Errorf("%w: actual state %d: %v", ErrInvalidJobResultEvidence, index, err)
		}
		switch state.State {
		case events.StatePresent, events.StateAbsent, events.StateUnknown:
		default:
			return fmt.Errorf("%w: actual state %d has unsupported state", ErrInvalidJobResultEvidence, index)
		}
		if err := validateResultTimestamp("actual state", state.ObservedAt, startedAt, completedAt, observedAt); err != nil {
			return fmt.Errorf("%w: actual state %d: %v", ErrInvalidJobResultEvidence, index, err)
		}
	}
	for index, health := range output.Health {
		if err := validateResultIdentity("health resource id", health.ResourceID); err != nil {
			return fmt.Errorf("%w: health %d: %v", ErrInvalidJobResultEvidence, index, err)
		}
		switch health.Status {
		case events.HealthHealthy, events.HealthDegraded, events.HealthFailed, events.HealthUnknown:
		default:
			return fmt.Errorf("%w: health %d has unsupported status", ErrInvalidJobResultEvidence, index)
		}
		if err := validateResultTimestamp("health check", health.CheckedAt, startedAt, completedAt, observedAt); err != nil {
			return fmt.Errorf("%w: health %d: %v", ErrInvalidJobResultEvidence, index, err)
		}
	}
	seenAuditIDs := make(map[string]struct{}, len(output.AuditEvents))
	for index, audit := range output.AuditEvents {
		for name, value := range map[string]string{
			"audit id": audit.ID, "audit actor": audit.Actor, "audit action": audit.Action,
			"audit outcome": audit.Outcome, "audit correlation id": audit.CorrelationID,
		} {
			if err := validateResultIdentity(name, value); err != nil {
				return fmt.Errorf("%w: audit event %d: %v", ErrInvalidJobResultEvidence, index, err)
			}
		}
		if audit.ResourceID != "" {
			if err := validateResultIdentity("audit resource id", audit.ResourceID); err != nil {
				return fmt.Errorf("%w: audit event %d: %v", ErrInvalidJobResultEvidence, index, err)
			}
		}
		if _, duplicate := seenAuditIDs[audit.ID]; duplicate {
			return fmt.Errorf("%w: duplicate audit event id %q", ErrInvalidJobResultEvidence, audit.ID)
		}
		seenAuditIDs[audit.ID] = struct{}{}
		if err := validateResultTimestamp("audit event", audit.OccurredAt, startedAt, completedAt, observedAt); err != nil {
			return fmt.Errorf("%w: audit event %d: %v", ErrInvalidJobResultEvidence, index, err)
		}
	}
	return nil
}

func validateResultTimestamp(name string, value, startedAt, completedAt, observedAt time.Time) error {
	if value.IsZero() {
		return fmt.Errorf("%s time is required", name)
	}
	value = value.UTC()
	if value.Before(startedAt) {
		return fmt.Errorf("%s predates Job creation", name)
	}
	if value.After(completedAt) || value.After(observedAt) {
		return fmt.Errorf("%s is newer than terminal Job evidence", name)
	}
	return nil
}

func digestResultOutput(output events.Output) (string, error) {
	canonical := events.Output{
		ActualStates: append([]events.ActualState(nil), output.ActualStates...),
		Health:       append([]events.Health(nil), output.Health...),
		AuditEvents:  append([]events.AuditEvent(nil), output.AuditEvents...),
	}
	sort.Slice(canonical.ActualStates, func(i, j int) bool {
		left, right := canonical.ActualStates[i], canonical.ActualStates[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.ResourceID != right.ResourceID {
			return left.ResourceID < right.ResourceID
		}
		if !left.ObservedAt.Equal(right.ObservedAt) {
			return left.ObservedAt.Before(right.ObservedAt)
		}
		if left.State != right.State {
			return left.State < right.State
		}
		if left.Revision != right.Revision {
			return left.Revision < right.Revision
		}
		return string(left.Details) < string(right.Details)
	})
	sort.Slice(canonical.Health, func(i, j int) bool {
		left, right := canonical.Health[i], canonical.Health[j]
		if left.ResourceID != right.ResourceID {
			return left.ResourceID < right.ResourceID
		}
		if !left.CheckedAt.Equal(right.CheckedAt) {
			return left.CheckedAt.Before(right.CheckedAt)
		}
		if left.Status != right.Status {
			return left.Status < right.Status
		}
		return left.Message < right.Message
	})
	sort.Slice(canonical.AuditEvents, func(i, j int) bool {
		left, right := canonical.AuditEvents[i], canonical.AuditEvents[j]
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		return left.OccurredAt.Before(right.OccurredAt)
	})
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("%w: encode output evidence: %v", ErrInvalidJobResultEvidence, err)
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validateResultIdentity(name, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed != value || len(value) > MaxJobResultEvidenceIdentifierLength {
		return fmt.Errorf("%w: canonical %s is required", ErrInvalidJobResultEvidence, name)
	}
	return nil
}

func validResultDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, ch := range value[len("sha256:"):] {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func worseResultHealth(left, right events.HealthStatus) events.HealthStatus {
	rank := map[events.HealthStatus]int{
		"":                    0,
		events.HealthHealthy:  1,
		events.HealthUnknown:  2,
		events.HealthDegraded: 3,
		events.HealthFailed:   4,
	}
	if rank[right] > rank[left] {
		return right
	}
	return left
}
