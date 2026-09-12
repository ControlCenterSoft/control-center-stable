package ui

import (
	"fmt"
	"strings"
	"time"

	"control-center/internal/orchestration/job"
	"control-center/internal/orchestration/operationsview"
)

// WorkflowEvidenceSummary is the bounded operator-facing projection of one
// validated end-to-end workflow evidence chain. It deliberately carries only
// identifiers, state and bounded blockers; raw Job output, errors, credentials,
// lease material and provider details remain outside the UI contract.
type WorkflowEvidenceSummary struct {
	Availability     EvidenceAvailability                           `json:"availability"`
	State            operationsview.OperationsWorkflowEvidenceState `json:"state,omitempty"`
	EvidenceComplete bool                                           `json:"evidence_complete"`
	JobID            string                                         `json:"job_id,omitempty"`
	JobVersion       uint64                                         `json:"job_version,omitempty"`
	Outcome          job.Status                                     `json:"outcome,omitempty"`
	BlockReasons     []operationsview.OperationsWorkflowBlockReason `json:"block_reasons"`
	ObservedAt       *time.Time                                     `json:"observed_at,omitempty"`
}

// ApplyVerifiedOperationsWorkflowEvidence attaches only validated, exact-
// revision and exact-Job workflow evidence to an already-built Changes / Jobs
// view. Missing source data fails closed to unavailable. Contradictions between
// the aggregate evidence and the current operator projection are rejected
// rather than partially exposed.
func ApplyVerifiedOperationsWorkflowEvidence(
	view ChangesJobsView,
	revisionDigests map[string]string,
	evidence []operationsview.OperationsWorkflowEvidence,
	evidenceLoaded bool,
	now time.Time,
) (ChangesJobsView, error) {
	if err := ValidateChangesJobsView(view); err != nil {
		return ChangesJobsView{}, err
	}
	result := cloneChangesJobsView(view)
	for index := range result.Changes {
		result.Changes[index].WorkflowEvidence = unavailableWorkflowEvidenceSummary()
	}
	if !evidenceLoaded {
		if len(evidence) != 0 {
			return ChangesJobsView{}, fmt.Errorf("%w: workflow evidence supplied while source is not loaded", ErrInvalidChangesJobsView)
		}
		return result, nil
	}
	if result.State != ChangesJobsCurrent {
		return ChangesJobsView{}, fmt.Errorf("%w: workflow evidence cannot be attached to unavailable changes/jobs state", ErrInvalidChangesJobsView)
	}
	if now.IsZero() {
		return ChangesJobsView{}, fmt.Errorf("%w: current time is required for workflow evidence", ErrInvalidChangesJobsView)
	}
	now = now.UTC()

	changeIndex := make(map[string]int, len(result.Changes))
	for index, changeView := range result.Changes {
		changeIndex[changeView.ID] = index
	}
	seen := make(map[string]struct{}, len(evidence))
	for _, item := range evidence {
		if err := operationsview.ValidateOperationsWorkflowEvidence(item); err != nil {
			return ChangesJobsView{}, fmt.Errorf("%w: invalid workflow evidence: %v", ErrInvalidChangesJobsView, err)
		}
		if item.ObservedAt.After(now) {
			return ChangesJobsView{}, fmt.Errorf("%w: workflow evidence for change %q is future-dated", ErrInvalidChangesJobsView, item.ChangeID)
		}
		if _, duplicate := seen[item.ChangeID]; duplicate {
			return ChangesJobsView{}, fmt.Errorf("%w: duplicate workflow evidence for change %q", ErrInvalidChangesJobsView, item.ChangeID)
		}
		seen[item.ChangeID] = struct{}{}

		index, exists := changeIndex[item.ChangeID]
		if !exists {
			return ChangesJobsView{}, fmt.Errorf("%w: workflow evidence references unknown change %q", ErrInvalidChangesJobsView, item.ChangeID)
		}
		changeView := &result.Changes[index]
		if item.RevisionID != changeView.RevisionID {
			return ChangesJobsView{}, fmt.Errorf("%w: workflow evidence for change %q targets revision %q, current revision is %q", ErrInvalidChangesJobsView, item.ChangeID, item.RevisionID, changeView.RevisionID)
		}
		expectedDigest, exists := revisionDigests[item.RevisionID]
		if !exists || !validSHA256Digest(expectedDigest) {
			return ChangesJobsView{}, fmt.Errorf("%w: authoritative digest is unavailable for revision %q", ErrInvalidChangesJobsView, item.RevisionID)
		}
		if item.RevisionDigest != expectedDigest {
			return ChangesJobsView{}, fmt.Errorf("%w: workflow evidence digest mismatch for revision %q", ErrInvalidChangesJobsView, item.RevisionID)
		}
		if item.ApprovalSatisfied != changeView.Approvals.Satisfied {
			return ChangesJobsView{}, fmt.Errorf("%w: workflow approval state for change %q differs from current view", ErrInvalidChangesJobsView, item.ChangeID)
		}

		jobView, exists := exactWorkflowJob(changeView.Jobs, item.JobID)
		if !exists {
			return ChangesJobsView{}, fmt.Errorf("%w: workflow evidence for change %q references unknown job %q", ErrInvalidChangesJobsView, item.ChangeID, item.JobID)
		}
		if jobView.Version != item.JobVersion || jobView.Status != item.Outcome {
			return ChangesJobsView{}, fmt.Errorf("%w: workflow evidence for job %q is stale or has mismatched outcome", ErrInvalidChangesJobsView, item.JobID)
		}
		if jobView.Output.EvidenceAvailable != item.ResultOutputPresent ||
			jobView.Output.HealthChecks != item.HealthChecks ||
			jobView.Output.AuditEvents != item.AuditEvents ||
			jobView.Output.WorstHealth != item.WorstHealth {
			return ChangesJobsView{}, fmt.Errorf("%w: workflow result summary for job %q differs from current view", ErrInvalidChangesJobsView, item.JobID)
		}

		observedAt := item.ObservedAt.UTC()
		changeView.WorkflowEvidence = WorkflowEvidenceSummary{
			Availability:     EvidenceAvailable,
			State:            item.State,
			EvidenceComplete: item.EvidenceComplete,
			JobID:            item.JobID,
			JobVersion:       item.JobVersion,
			Outcome:          item.Outcome,
			BlockReasons:     append([]operationsview.OperationsWorkflowBlockReason(nil), item.BlockReasons...),
			ObservedAt:       &observedAt,
		}
	}
	if err := ValidateChangesJobsView(result); err != nil {
		return ChangesJobsView{}, err
	}
	return result, nil
}

func unavailableWorkflowEvidenceSummary() WorkflowEvidenceSummary {
	return WorkflowEvidenceSummary{
		Availability: EvidenceUnavailable,
		BlockReasons: []operationsview.OperationsWorkflowBlockReason{},
	}
}

func exactWorkflowJob(jobs []JobOperationalView, id string) (JobOperationalView, bool) {
	for _, candidate := range jobs {
		if candidate.ID == id {
			return candidate, true
		}
	}
	return JobOperationalView{}, false
}

func validateWorkflowEvidenceSummary(summary WorkflowEvidenceSummary, jobs []JobOperationalView) error {
	if summary.Availability != EvidenceUnavailable && summary.Availability != EvidenceAvailable {
		return fmt.Errorf("%w: invalid workflow evidence availability %q", ErrInvalidChangesJobsView, summary.Availability)
	}
	if summary.Availability == EvidenceUnavailable {
		if summary.State != "" || summary.EvidenceComplete || summary.JobID != "" || summary.JobVersion != 0 || summary.Outcome != "" || len(summary.BlockReasons) != 0 || summary.ObservedAt != nil {
			return fmt.Errorf("%w: unavailable workflow evidence must not carry authoritative data", ErrInvalidChangesJobsView)
		}
		return nil
	}
	if strings.TrimSpace(summary.JobID) == "" || summary.JobID != strings.TrimSpace(summary.JobID) || summary.JobVersion == 0 || summary.ObservedAt == nil || summary.ObservedAt.IsZero() {
		return fmt.Errorf("%w: available workflow evidence has incomplete identity", ErrInvalidChangesJobsView)
	}
	if !validJobStatus(summary.Outcome) || !summary.Outcome.Terminal() {
		return fmt.Errorf("%w: available workflow evidence has non-terminal outcome", ErrInvalidChangesJobsView)
	}

	switch summary.State {
	case operationsview.OperationsWorkflowEvidenceComplete:
		if !summary.EvidenceComplete || len(summary.BlockReasons) != 0 {
			return fmt.Errorf("%w: complete workflow evidence has inconsistent blockers", ErrInvalidChangesJobsView)
		}
	case operationsview.OperationsWorkflowEvidenceBlocked:
		if summary.EvidenceComplete || len(summary.BlockReasons) == 0 {
			return fmt.Errorf("%w: blocked workflow evidence must carry blockers", ErrInvalidChangesJobsView)
		}
	default:
		return fmt.Errorf("%w: available workflow evidence has invalid state %q", ErrInvalidChangesJobsView, summary.State)
	}

	seenReasons := make(map[operationsview.OperationsWorkflowBlockReason]struct{}, len(summary.BlockReasons))
	for _, reason := range summary.BlockReasons {
		if !validWorkflowBlockReason(reason) {
			return fmt.Errorf("%w: workflow evidence has invalid blocker %q", ErrInvalidChangesJobsView, reason)
		}
		if _, duplicate := seenReasons[reason]; duplicate {
			return fmt.Errorf("%w: workflow evidence has duplicate blocker %q", ErrInvalidChangesJobsView, reason)
		}
		seenReasons[reason] = struct{}{}
	}

	jobView, exists := exactWorkflowJob(jobs, summary.JobID)
	if !exists || jobView.Version != summary.JobVersion || jobView.Status != summary.Outcome {
		return fmt.Errorf("%w: workflow evidence does not match current job projection", ErrInvalidChangesJobsView)
	}
	return nil
}

func validWorkflowBlockReason(reason operationsview.OperationsWorkflowBlockReason) bool {
	switch reason {
	case operationsview.OperationsWorkflowBlockApproval,
		operationsview.OperationsWorkflowBlockRecovery,
		operationsview.OperationsWorkflowBlockVerification,
		operationsview.OperationsWorkflowBlockAudit:
		return true
	default:
		return false
	}
}
