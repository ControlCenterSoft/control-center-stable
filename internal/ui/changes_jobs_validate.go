package ui

import (
	"fmt"

	"control-center/internal/orchestration/events"
	"control-center/internal/orchestration/policy"
)

// ValidateChangesJobsView rejects inconsistent provider output before it is
// exposed through an operator surface. Unavailable sources must never be
// represented as a successful empty list.
func ValidateChangesJobsView(view ChangesJobsView) error {
	if view.ContractVersion != ChangesJobsContractVersion {
		return fmt.Errorf("%w: unsupported contract_version %q", ErrInvalidChangesJobsView, view.ContractVersion)
	}
	if view.State != ChangesJobsUnavailable && view.State != ChangesJobsCurrent {
		return fmt.Errorf("%w: unsupported view state %q", ErrInvalidChangesJobsView, view.State)
	}
	if view.State == ChangesJobsUnavailable {
		if view.GeneratedAt != nil || view.ChangeCount != 0 || view.JobCount != 0 || len(view.Changes) != 0 {
			return fmt.Errorf("%w: unavailable view must not carry authoritative data", ErrInvalidChangesJobsView)
		}
		return nil
	}
	if view.GeneratedAt == nil || view.GeneratedAt.IsZero() {
		return fmt.Errorf("%w: generated_at is required for loaded state", ErrInvalidChangesJobsView)
	}
	if view.ChangeCount != len(view.Changes) {
		return fmt.Errorf("%w: change_count=%d does not match changes=%d", ErrInvalidChangesJobsView, view.ChangeCount, len(view.Changes))
	}

	changeIDs := make(map[string]struct{}, len(view.Changes))
	jobIDs := make(map[string]struct{}, view.JobCount)
	totalJobs := 0
	for _, changeView := range view.Changes {
		if changeView.ID == "" || changeView.Action == "" || changeView.Requester == "" || changeView.RevisionID == "" || changeView.Version == 0 || changeView.UpdatedAt.IsZero() {
			return fmt.Errorf("%w: change identity/version evidence is incomplete", ErrInvalidChangesJobsView)
		}
		if _, duplicate := changeIDs[changeView.ID]; duplicate {
			return fmt.Errorf("%w: duplicate change %q", ErrInvalidChangesJobsView, changeView.ID)
		}
		changeIDs[changeView.ID] = struct{}{}
		if !changeView.Risk.Valid() || !validChangeState(changeView.State) {
			return fmt.Errorf("%w: change %q has invalid risk/state", ErrInvalidChangesJobsView, changeView.ID)
		}
		if changeView.PolicyEffect != policy.EffectAllow && changeView.PolicyEffect != policy.EffectDeny {
			return fmt.Errorf("%w: change %q has invalid policy effect", ErrInvalidChangesJobsView, changeView.ID)
		}
		if changeView.Approvals.Required < 0 || changeView.Approvals.Provided < 0 || (changeView.Approvals.Satisfied && changeView.Approvals.Provided < changeView.Approvals.Required) {
			return fmt.Errorf("%w: change %q has inconsistent approval summary", ErrInvalidChangesJobsView, changeView.ID)
		}
		for field, evidence := range map[string]EvidenceAvailability{
			"semantic_diff":      changeView.SemanticDiff,
			"blast_radius":       changeView.BlastRadius,
			"maintenance_window": changeView.MaintenanceWindow,
			"recovery_evidence":  changeView.RecoveryEvidence,
		} {
			if evidence != EvidenceUnavailable && evidence != EvidenceAvailable {
				return fmt.Errorf("%w: change %q has invalid %s availability %q", ErrInvalidChangesJobsView, changeView.ID, field, evidence)
			}
		}
		for _, jobView := range changeView.Jobs {
			totalJobs++
			if jobView.ID == "" || jobView.ActionName != changeView.Action || !validJobStatus(jobView.Status) || jobView.Version == 0 {
				return fmt.Errorf("%w: change %q has invalid job %q", ErrInvalidChangesJobsView, changeView.ID, jobView.ID)
			}
			if _, duplicate := jobIDs[jobView.ID]; duplicate {
				return fmt.Errorf("%w: duplicate job %q", ErrInvalidChangesJobsView, jobView.ID)
			}
			jobIDs[jobView.ID] = struct{}{}
			if jobView.MaxAttempts <= 0 || jobView.Attempt < 0 || jobView.Attempt > jobView.MaxAttempts || jobView.CreatedAt.IsZero() || jobView.UpdatedAt.IsZero() || jobView.UpdatedAt.Before(jobView.CreatedAt) {
				return fmt.Errorf("%w: job %q has inconsistent execution counters/timestamps", ErrInvalidChangesJobsView, jobView.ID)
			}
			if jobView.Output.ActualStates < 0 || jobView.Output.HealthChecks < 0 || jobView.Output.AuditEvents < 0 {
				return fmt.Errorf("%w: job %q has negative output counters", ErrInvalidChangesJobsView, jobView.ID)
			}
			if !jobView.Output.EvidenceAvailable {
				if jobView.Output.ActualStates != 0 || jobView.Output.HealthChecks != 0 || jobView.Output.AuditEvents != 0 || jobView.Output.WorstHealth != "" {
					return fmt.Errorf("%w: job %q has output details without available evidence", ErrInvalidChangesJobsView, jobView.ID)
				}
			} else if !validProjectedHealth(jobView.Output.WorstHealth) {
				return fmt.Errorf("%w: job %q has unsupported health summary %q", ErrInvalidChangesJobsView, jobView.ID, jobView.Output.WorstHealth)
			}
		}
		if err := validateWorkflowEvidenceSummary(changeView.WorkflowEvidence, changeView.Jobs); err != nil {
			return fmt.Errorf("%w: change %q workflow evidence is invalid: %v", ErrInvalidChangesJobsView, changeView.ID, err)
		}
	}
	if view.JobCount != totalJobs {
		return fmt.Errorf("%w: job_count=%d does not match jobs=%d", ErrInvalidChangesJobsView, view.JobCount, totalJobs)
	}
	return nil
}

func validProjectedHealth(status events.HealthStatus) bool {
	switch status {
	case "", events.HealthHealthy, events.HealthUnknown, events.HealthDegraded, events.HealthFailed:
		return true
	default:
		return false
	}
}
