package ui

import (
	"fmt"
	"time"

	"control-center/internal/orchestration/operationsview"
)

// ApplyVerifiedMaintenanceWindowEvidence projects maintenance-window evidence
// only after the typed contract is validated and bound to the exact immutable
// Change revision. It is read-only and never grants approval, scheduling or
// execution authority.
func ApplyVerifiedMaintenanceWindowEvidence(
	view ChangesJobsView,
	revisionDigests map[string]string,
	evidence []operationsview.MaintenanceWindowEvidence,
	evidenceLoaded bool,
	now time.Time,
) (ChangesJobsView, error) {
	if err := ValidateChangesJobsView(view); err != nil {
		return ChangesJobsView{}, err
	}
	result := cloneChangesJobsView(view)
	for index := range result.Changes {
		result.Changes[index].MaintenanceWindow = EvidenceUnavailable
	}
	if !evidenceLoaded {
		if len(evidence) != 0 {
			return ChangesJobsView{}, fmt.Errorf("%w: maintenance window evidence supplied while source is not loaded", ErrInvalidChangesJobsView)
		}
		return result, nil
	}
	if result.State != ChangesJobsCurrent {
		return ChangesJobsView{}, fmt.Errorf("%w: maintenance window evidence cannot be attached to unavailable changes/jobs state", ErrInvalidChangesJobsView)
	}
	if now.IsZero() {
		return ChangesJobsView{}, fmt.Errorf("%w: current time is required for maintenance window evidence", ErrInvalidChangesJobsView)
	}
	now = now.UTC()

	changeIndex := make(map[string]int, len(result.Changes))
	for index, changeView := range result.Changes {
		changeIndex[changeView.ID] = index
	}
	seen := make(map[string]struct{}, len(evidence))
	for _, item := range evidence {
		if err := operationsview.ValidateMaintenanceWindowEvidence(item); err != nil {
			return ChangesJobsView{}, fmt.Errorf("%w: invalid maintenance window evidence: %v", ErrInvalidChangesJobsView, err)
		}
		if item.EvaluatedAt.After(now) {
			return ChangesJobsView{}, fmt.Errorf("%w: maintenance window evidence for change %q is future-dated", ErrInvalidChangesJobsView, item.ChangeID)
		}
		if _, duplicate := seen[item.ChangeID]; duplicate {
			return ChangesJobsView{}, fmt.Errorf("%w: duplicate maintenance window evidence for change %q", ErrInvalidChangesJobsView, item.ChangeID)
		}
		seen[item.ChangeID] = struct{}{}

		index, exists := changeIndex[item.ChangeID]
		if !exists {
			return ChangesJobsView{}, fmt.Errorf("%w: maintenance window evidence references unknown change %q", ErrInvalidChangesJobsView, item.ChangeID)
		}
		changeView := &result.Changes[index]
		if item.RevisionID != changeView.RevisionID {
			return ChangesJobsView{}, fmt.Errorf("%w: maintenance window evidence for change %q targets revision %q, current revision is %q", ErrInvalidChangesJobsView, item.ChangeID, item.RevisionID, changeView.RevisionID)
		}
		expectedDigest, exists := revisionDigests[item.RevisionID]
		if !exists || !validSHA256Digest(expectedDigest) {
			return ChangesJobsView{}, fmt.Errorf("%w: authoritative digest is unavailable for revision %q", ErrInvalidChangesJobsView, item.RevisionID)
		}
		if item.RevisionDigest != expectedDigest {
			return ChangesJobsView{}, fmt.Errorf("%w: maintenance window digest mismatch for revision %q", ErrInvalidChangesJobsView, item.RevisionID)
		}
		changeView.MaintenanceWindow = EvidenceAvailable
	}
	if err := ValidateChangesJobsView(result); err != nil {
		return ChangesJobsView{}, err
	}
	return result, nil
}
