package ui

import (
	"fmt"
	"time"

	"control-center/internal/orchestration/operationsview"
)

// ApplyVerifiedBlastRadiusEvidence makes blast-radius availability visible only
// after a typed evidence contract has been validated and bound to the exact
// immutable Change revision. The function is read-only and cannot approve,
// schedule, queue or execute a Change.
func ApplyVerifiedBlastRadiusEvidence(
	view ChangesJobsView,
	revisionDigests map[string]string,
	evidence []operationsview.BlastRadiusEvidence,
	evidenceLoaded bool,
	now time.Time,
) (ChangesJobsView, error) {
	if err := ValidateChangesJobsView(view); err != nil {
		return ChangesJobsView{}, err
	}
	result := cloneChangesJobsView(view)
	for index := range result.Changes {
		result.Changes[index].BlastRadius = EvidenceUnavailable
	}
	if !evidenceLoaded {
		if len(evidence) != 0 {
			return ChangesJobsView{}, fmt.Errorf("%w: blast radius evidence supplied while source is not loaded", ErrInvalidChangesJobsView)
		}
		return result, nil
	}
	if result.State != ChangesJobsCurrent {
		return ChangesJobsView{}, fmt.Errorf("%w: blast radius evidence cannot be attached to unavailable changes/jobs state", ErrInvalidChangesJobsView)
	}
	if now.IsZero() {
		return ChangesJobsView{}, fmt.Errorf("%w: current time is required for blast radius evidence", ErrInvalidChangesJobsView)
	}
	now = now.UTC()

	changeIndex := make(map[string]int, len(result.Changes))
	for index, changeView := range result.Changes {
		changeIndex[changeView.ID] = index
	}
	seen := make(map[string]struct{}, len(evidence))
	for _, item := range evidence {
		if err := operationsview.ValidateBlastRadiusEvidence(item); err != nil {
			return ChangesJobsView{}, fmt.Errorf("%w: invalid blast radius evidence: %v", ErrInvalidChangesJobsView, err)
		}
		if item.ObservedAt.After(now) {
			return ChangesJobsView{}, fmt.Errorf("%w: blast radius evidence for change %q is future-dated", ErrInvalidChangesJobsView, item.ChangeID)
		}
		if _, duplicate := seen[item.ChangeID]; duplicate {
			return ChangesJobsView{}, fmt.Errorf("%w: duplicate blast radius evidence for change %q", ErrInvalidChangesJobsView, item.ChangeID)
		}
		seen[item.ChangeID] = struct{}{}

		index, exists := changeIndex[item.ChangeID]
		if !exists {
			return ChangesJobsView{}, fmt.Errorf("%w: blast radius evidence references unknown change %q", ErrInvalidChangesJobsView, item.ChangeID)
		}
		changeView := &result.Changes[index]
		if item.RevisionID != changeView.RevisionID {
			return ChangesJobsView{}, fmt.Errorf("%w: blast radius evidence for change %q targets revision %q, current revision is %q", ErrInvalidChangesJobsView, item.ChangeID, item.RevisionID, changeView.RevisionID)
		}
		expectedDigest, exists := revisionDigests[item.RevisionID]
		if !exists || !validSHA256Digest(expectedDigest) {
			return ChangesJobsView{}, fmt.Errorf("%w: authoritative digest is unavailable for revision %q", ErrInvalidChangesJobsView, item.RevisionID)
		}
		if item.RevisionDigest != expectedDigest {
			return ChangesJobsView{}, fmt.Errorf("%w: blast radius digest mismatch for revision %q", ErrInvalidChangesJobsView, item.RevisionID)
		}
		changeView.BlastRadius = EvidenceAvailable
	}
	if err := ValidateChangesJobsView(result); err != nil {
		return ChangesJobsView{}, err
	}
	return result, nil
}
