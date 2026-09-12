package ui

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"control-center/internal/orchestration/operationsview"
)

// ChangeEvidenceSnapshot carries operator-safe availability evidence for one
// exact immutable configuration revision. It is read-only metadata: it does
// not grant approval, scheduling, retry, cancellation or execution authority.
type ChangeEvidenceSnapshot struct {
	ChangeID          string
	RevisionID        string
	RevisionDigest    string
	SemanticDiff      EvidenceAvailability
	BlastRadius       EvidenceAvailability
	MaintenanceWindow EvidenceAvailability
	RecoveryEvidence  EvidenceAvailability
	ObservedAt        time.Time
}

// ApplyChangeEvidence binds read-only operator evidence to an already-built
// Changes / Jobs view. Evidence is accepted only when it matches both the
// change revision id and the authoritative SHA-256 digest supplied by the
// revision store. Unknown, malformed, future-dated or mismatched evidence is
// rejected without returning a partially enriched view.
func ApplyChangeEvidence(
	view ChangesJobsView,
	revisionDigests map[string]string,
	snapshots []ChangeEvidenceSnapshot,
	evidenceLoaded bool,
	now time.Time,
) (ChangesJobsView, error) {
	if err := ValidateChangesJobsView(view); err != nil {
		return ChangesJobsView{}, err
	}
	result := cloneChangesJobsView(view)
	if !evidenceLoaded {
		if len(snapshots) != 0 {
			return ChangesJobsView{}, fmt.Errorf("%w: evidence snapshots supplied while source is not loaded", ErrInvalidChangesJobsView)
		}
		return result, nil
	}
	if view.State != ChangesJobsCurrent {
		return ChangesJobsView{}, fmt.Errorf("%w: evidence cannot be attached to unavailable changes/jobs state", ErrInvalidChangesJobsView)
	}
	if now.IsZero() {
		return ChangesJobsView{}, fmt.Errorf("%w: current time is required for evidence validation", ErrInvalidChangesJobsView)
	}
	now = now.UTC()

	changeIndex := make(map[string]int, len(result.Changes))
	for index, changeView := range result.Changes {
		changeIndex[changeView.ID] = index
	}
	seen := make(map[string]struct{}, len(snapshots))
	for _, snapshot := range snapshots {
		if err := validateChangeEvidenceSnapshot(snapshot, now); err != nil {
			return ChangesJobsView{}, err
		}
		if _, duplicate := seen[snapshot.ChangeID]; duplicate {
			return ChangesJobsView{}, fmt.Errorf("%w: duplicate evidence for change %q", ErrInvalidChangesJobsView, snapshot.ChangeID)
		}
		seen[snapshot.ChangeID] = struct{}{}

		index, exists := changeIndex[snapshot.ChangeID]
		if !exists {
			return ChangesJobsView{}, fmt.Errorf("%w: evidence references unknown change %q", ErrInvalidChangesJobsView, snapshot.ChangeID)
		}
		changeView := &result.Changes[index]
		if snapshot.RevisionID != changeView.RevisionID {
			return ChangesJobsView{}, fmt.Errorf("%w: evidence for change %q targets revision %q, current revision is %q", ErrInvalidChangesJobsView, snapshot.ChangeID, snapshot.RevisionID, changeView.RevisionID)
		}
		expectedDigest, exists := revisionDigests[snapshot.RevisionID]
		if !exists || !validSHA256Digest(expectedDigest) {
			return ChangesJobsView{}, fmt.Errorf("%w: authoritative digest is unavailable for revision %q", ErrInvalidChangesJobsView, snapshot.RevisionID)
		}
		if snapshot.RevisionDigest != expectedDigest {
			return ChangesJobsView{}, fmt.Errorf("%w: evidence digest mismatch for revision %q", ErrInvalidChangesJobsView, snapshot.RevisionID)
		}

		changeView.SemanticDiff = snapshot.SemanticDiff
		changeView.BlastRadius = snapshot.BlastRadius
		changeView.MaintenanceWindow = snapshot.MaintenanceWindow
		changeView.RecoveryEvidence = snapshot.RecoveryEvidence
	}
	if err := ValidateChangesJobsView(result); err != nil {
		return ChangesJobsView{}, err
	}
	return result, nil
}

func validateChangeEvidenceSnapshot(snapshot ChangeEvidenceSnapshot, now time.Time) error {
	for field, value := range map[string]string{
		"change_id":       snapshot.ChangeID,
		"revision_id":     snapshot.RevisionID,
		"revision_digest": snapshot.RevisionDigest,
	} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("%w: evidence has invalid %s", ErrInvalidChangesJobsView, field)
		}
	}
	if !validSHA256Digest(snapshot.RevisionDigest) {
		return fmt.Errorf("%w: evidence for change %q has invalid revision digest", ErrInvalidChangesJobsView, snapshot.ChangeID)
	}
	if snapshot.ObservedAt.IsZero() || snapshot.ObservedAt.After(now) {
		return fmt.Errorf("%w: evidence for change %q has invalid observed_at", ErrInvalidChangesJobsView, snapshot.ChangeID)
	}
	for field, availability := range map[string]EvidenceAvailability{
		"semantic_diff":      snapshot.SemanticDiff,
		"blast_radius":       snapshot.BlastRadius,
		"maintenance_window": snapshot.MaintenanceWindow,
		"recovery_evidence":  snapshot.RecoveryEvidence,
	} {
		if availability != EvidenceUnavailable && availability != EvidenceAvailable {
			return fmt.Errorf("%w: evidence for change %q has invalid %s availability %q", ErrInvalidChangesJobsView, snapshot.ChangeID, field, availability)
		}
	}
	return nil
}

func validSHA256Digest(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	digest := strings.TrimPrefix(value, prefix)
	if len(digest) != 64 || digest != strings.ToLower(digest) {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func cloneChangesJobsView(view ChangesJobsView) ChangesJobsView {
	result := view
	if view.GeneratedAt != nil {
		generatedAt := *view.GeneratedAt
		result.GeneratedAt = &generatedAt
	}
	result.Changes = append([]ChangeOperationalView(nil), view.Changes...)
	for index := range result.Changes {
		result.Changes[index].Jobs = append([]JobOperationalView(nil), view.Changes[index].Jobs...)
		result.Changes[index].WorkflowEvidence.BlockReasons = append(
			[]operationsview.OperationsWorkflowBlockReason(nil),
			view.Changes[index].WorkflowEvidence.BlockReasons...,
		)
		if view.Changes[index].WorkflowEvidence.ObservedAt != nil {
			observedAt := *view.Changes[index].WorkflowEvidence.ObservedAt
			result.Changes[index].WorkflowEvidence.ObservedAt = &observedAt
		}
	}
	return result
}
