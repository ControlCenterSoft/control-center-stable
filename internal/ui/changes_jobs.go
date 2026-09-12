package ui

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"control-center/internal/orchestration/change"
	"control-center/internal/orchestration/events"
	"control-center/internal/orchestration/job"
	"control-center/internal/orchestration/policy"
)

const ChangesJobsContractVersion = "ui.changes-jobs/v1"

var ErrInvalidChangesJobsView = errors.New("invalid changes/jobs view")

type ChangesJobsViewState string

const (
	ChangesJobsUnavailable ChangesJobsViewState = "unavailable"
	ChangesJobsCurrent     ChangesJobsViewState = "current"
)

type ChangesJobsInput struct {
	Changes       []change.Snapshot
	Jobs          []job.Job
	ChangesLoaded bool
	JobsLoaded    bool
	Now           time.Time
}

type ChangesJobsView struct {
	ContractVersion string                  `json:"contract_version"`
	State           ChangesJobsViewState    `json:"state"`
	GeneratedAt     *time.Time              `json:"generated_at,omitempty"`
	ChangeCount     int                     `json:"change_count"`
	JobCount        int                     `json:"job_count"`
	Changes         []ChangeOperationalView `json:"changes"`
}

type ApprovalSummary struct {
	Required  int  `json:"required"`
	Provided  int  `json:"provided"`
	Satisfied bool `json:"satisfied"`
}

type ChangeOperationalView struct {
	ID                string                  `json:"id"`
	Action            string                  `json:"action"`
	Requester         string                  `json:"requester"`
	RevisionID        string                  `json:"revision_id"`
	Risk              policy.Risk             `json:"risk"`
	State             change.State            `json:"state"`
	PolicyEffect      policy.Effect           `json:"policy_effect"`
	Approvals         ApprovalSummary         `json:"approvals"`
	Version           uint64                  `json:"version"`
	UpdatedAt         time.Time               `json:"updated_at"`
	SemanticDiff      EvidenceAvailability    `json:"semantic_diff"`
	BlastRadius       EvidenceAvailability    `json:"blast_radius"`
	MaintenanceWindow EvidenceAvailability    `json:"maintenance_window"`
	RecoveryEvidence  EvidenceAvailability    `json:"recovery_evidence"`
	WorkflowEvidence  WorkflowEvidenceSummary `json:"workflow_evidence"`
	Jobs              []JobOperationalView    `json:"jobs"`
}

type JobOutputSummary struct {
	EvidenceAvailable bool                `json:"evidence_available"`
	ActualStates      int                 `json:"actual_states"`
	HealthChecks      int                 `json:"health_checks"`
	AuditEvents       int                 `json:"audit_events"`
	WorstHealth       events.HealthStatus `json:"worst_health,omitempty"`
}

type JobOperationalView struct {
	ID              string           `json:"id"`
	ActionName      string           `json:"action_name"`
	Status          job.Status       `json:"status"`
	Attempt         int              `json:"attempt"`
	MaxAttempts     int              `json:"max_attempts"`
	NextAttemptAt   *time.Time       `json:"next_attempt_at,omitempty"`
	LeaseHeld       bool             `json:"lease_held"`
	FailureRecorded bool             `json:"failure_recorded"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
	Version         uint64           `json:"version"`
	Output          JobOutputSummary `json:"output"`
}

// BuildChangesJobsView projects existing durable orchestration state into a
// minimal operator-safe read model. Raw job input, idempotency keys, lease
// tokens/worker IDs, raw errors and output details are deliberately excluded.
func BuildChangesJobsView(input ChangesJobsInput) (ChangesJobsView, error) {
	view := ChangesJobsView{
		ContractVersion: ChangesJobsContractVersion,
		State:           ChangesJobsUnavailable,
		Changes:         []ChangeOperationalView{},
	}
	if !input.ChangesLoaded || !input.JobsLoaded {
		return view, nil
	}
	if input.Now.IsZero() {
		return ChangesJobsView{}, fmt.Errorf("%w: current time is required", ErrInvalidChangesJobsView)
	}
	now := input.Now.UTC()
	view.GeneratedAt = &now
	view.State = ChangesJobsCurrent

	changesByID := make(map[string]int, len(input.Changes))
	for _, snapshot := range input.Changes {
		projected, err := projectChange(snapshot)
		if err != nil {
			return ChangesJobsView{}, err
		}
		if _, exists := changesByID[projected.ID]; exists {
			return ChangesJobsView{}, fmt.Errorf("%w: duplicate change %q", ErrInvalidChangesJobsView, projected.ID)
		}
		changesByID[projected.ID] = len(view.Changes)
		view.Changes = append(view.Changes, projected)
	}

	seenJobs := make(map[string]struct{}, len(input.Jobs))
	for _, source := range input.Jobs {
		projected, err := projectJob(source, now)
		if err != nil {
			return ChangesJobsView{}, err
		}
		if _, exists := seenJobs[projected.ID]; exists {
			return ChangesJobsView{}, fmt.Errorf("%w: duplicate job %q", ErrInvalidChangesJobsView, projected.ID)
		}
		seenJobs[projected.ID] = struct{}{}
		changeIndex, exists := changesByID[source.ChangeID]
		if !exists {
			return ChangesJobsView{}, fmt.Errorf("%w: job %q references unknown change %q", ErrInvalidChangesJobsView, source.ID, source.ChangeID)
		}
		if source.ActionName != view.Changes[changeIndex].Action {
			return ChangesJobsView{}, fmt.Errorf("%w: job %q action %q differs from change action %q", ErrInvalidChangesJobsView, source.ID, source.ActionName, view.Changes[changeIndex].Action)
		}
		view.Changes[changeIndex].Jobs = append(view.Changes[changeIndex].Jobs, projected)
		view.JobCount++
	}

	for index := range view.Changes {
		sort.Slice(view.Changes[index].Jobs, func(i, j int) bool {
			left, right := view.Changes[index].Jobs[i], view.Changes[index].Jobs[j]
			if !left.UpdatedAt.Equal(right.UpdatedAt) {
				return left.UpdatedAt.After(right.UpdatedAt)
			}
			return left.ID < right.ID
		})
	}
	sort.Slice(view.Changes, func(i, j int) bool {
		if !view.Changes[i].UpdatedAt.Equal(view.Changes[j].UpdatedAt) {
			return view.Changes[i].UpdatedAt.After(view.Changes[j].UpdatedAt)
		}
		return view.Changes[i].ID < view.Changes[j].ID
	})
	view.ChangeCount = len(view.Changes)
	return view, nil
}

func projectChange(snapshot change.Snapshot) (ChangeOperationalView, error) {
	if strings.TrimSpace(snapshot.ID) == "" || snapshot.ID != strings.TrimSpace(snapshot.ID) {
		return ChangeOperationalView{}, fmt.Errorf("%w: invalid change id", ErrInvalidChangesJobsView)
	}
	if strings.TrimSpace(snapshot.Action) == "" || snapshot.Action != strings.TrimSpace(snapshot.Action) {
		return ChangeOperationalView{}, fmt.Errorf("%w: change %q has invalid action", ErrInvalidChangesJobsView, snapshot.ID)
	}
	if strings.TrimSpace(snapshot.Requester) == "" || snapshot.Requester != strings.TrimSpace(snapshot.Requester) {
		return ChangeOperationalView{}, fmt.Errorf("%w: change %q has invalid requester", ErrInvalidChangesJobsView, snapshot.ID)
	}
	if strings.TrimSpace(snapshot.RevisionID) == "" || snapshot.RevisionID != strings.TrimSpace(snapshot.RevisionID) {
		return ChangeOperationalView{}, fmt.Errorf("%w: change %q has invalid revision id", ErrInvalidChangesJobsView, snapshot.ID)
	}
	if snapshot.Version == 0 || snapshot.UpdatedAt.IsZero() || !validChangeState(snapshot.State) {
		return ChangeOperationalView{}, fmt.Errorf("%w: change %q has incomplete state evidence", ErrInvalidChangesJobsView, snapshot.ID)
	}
	if err := snapshot.Decision.Validate(); err != nil || snapshot.Risk != snapshot.Decision.Risk {
		return ChangeOperationalView{}, fmt.Errorf("%w: change %q has invalid policy evidence", ErrInvalidChangesJobsView, snapshot.ID)
	}
	approvalSatisfied := policy.CheckApprovals(snapshot.Requester, snapshot.Decision.Requirement, snapshot.Approvals) == nil
	return ChangeOperationalView{
		ID:                snapshot.ID,
		Action:            snapshot.Action,
		Requester:         snapshot.Requester,
		RevisionID:        snapshot.RevisionID,
		Risk:              snapshot.Risk,
		State:             snapshot.State,
		PolicyEffect:      snapshot.Decision.Effect,
		Approvals:         ApprovalSummary{Required: snapshot.Decision.Requirement.Minimum, Provided: len(snapshot.Approvals), Satisfied: approvalSatisfied},
		Version:           snapshot.Version,
		UpdatedAt:         snapshot.UpdatedAt.UTC(),
		SemanticDiff:      EvidenceUnavailable,
		BlastRadius:       EvidenceUnavailable,
		MaintenanceWindow: EvidenceUnavailable,
		RecoveryEvidence:  EvidenceUnavailable,
		WorkflowEvidence:  unavailableWorkflowEvidenceSummary(),
		Jobs:              []JobOperationalView{},
	}, nil
}

func projectJob(source job.Job, now time.Time) (JobOperationalView, error) {
	if strings.TrimSpace(source.ID) == "" || strings.TrimSpace(source.ChangeID) == "" || strings.TrimSpace(source.ActionName) == "" {
		return JobOperationalView{}, fmt.Errorf("%w: job identity fields are required", ErrInvalidChangesJobsView)
	}
	if source.ID != strings.TrimSpace(source.ID) || source.ChangeID != strings.TrimSpace(source.ChangeID) || source.ActionName != strings.TrimSpace(source.ActionName) {
		return JobOperationalView{}, fmt.Errorf("%w: job %q identity fields are not canonical", ErrInvalidChangesJobsView, source.ID)
	}
	if !validJobStatus(source.Status) || source.MaxAttempts <= 0 || source.Attempt < 0 || source.Attempt > source.MaxAttempts || source.Version == 0 {
		return JobOperationalView{}, fmt.Errorf("%w: job %q has invalid execution state", ErrInvalidChangesJobsView, source.ID)
	}
	if source.CreatedAt.IsZero() || source.UpdatedAt.IsZero() || source.UpdatedAt.Before(source.CreatedAt) {
		return JobOperationalView{}, fmt.Errorf("%w: job %q has invalid timestamps", ErrInvalidChangesJobsView, source.ID)
	}
	projected := JobOperationalView{
		ID:              source.ID,
		ActionName:      source.ActionName,
		Status:          source.Status,
		Attempt:         source.Attempt,
		MaxAttempts:     source.MaxAttempts,
		LeaseHeld:       source.Lease != nil && source.Lease.ExpiresAt.After(now),
		FailureRecorded: strings.TrimSpace(source.LastError) != "",
		CreatedAt:       source.CreatedAt.UTC(),
		UpdatedAt:       source.UpdatedAt.UTC(),
		Version:         source.Version,
		Output:          summarizeJobOutput(source.Output),
	}
	if !source.NextAttemptAt.IsZero() {
		next := source.NextAttemptAt.UTC()
		projected.NextAttemptAt = &next
	}
	return projected, nil
}

func summarizeJobOutput(output *events.Output) JobOutputSummary {
	if output == nil {
		return JobOutputSummary{}
	}
	summary := JobOutputSummary{
		EvidenceAvailable: true,
		ActualStates:      len(output.ActualStates),
		HealthChecks:      len(output.Health),
		AuditEvents:       len(output.AuditEvents),
	}
	for _, health := range output.Health {
		summary.WorstHealth = worseHealth(summary.WorstHealth, health.Status)
	}
	return summary
}

func worseHealth(left, right events.HealthStatus) events.HealthStatus {
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

func validChangeState(state change.State) bool {
	switch state {
	case change.StatePendingApproval, change.StateApproved, change.StateQueued, change.StateExecuting,
		change.StateVerifying, change.StateSucceeded, change.StateFailed, change.StateCancelled, change.StateRejected:
		return true
	default:
		return false
	}
}

func validJobStatus(status job.Status) bool {
	switch status {
	case job.StatusQueued, job.StatusRunning, job.StatusRetryWait, job.StatusCancelRequested,
		job.StatusCancelled, job.StatusSucceeded, job.StatusFailed:
		return true
	default:
		return false
	}
}
