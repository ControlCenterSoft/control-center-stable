package recovery

import (
	"fmt"
	"time"
)

const RestoreDrillEvidenceBindingStatusSchemaVersion = "recovery.restore-drill-evidence-binding-status/v1"

type RestoreDrillEvidenceBindingStatusState string

const (
	RestoreDrillEvidenceBindingCurrent    RestoreDrillEvidenceBindingStatusState = "current"
	RestoreDrillEvidenceBindingNotCurrent RestoreDrillEvidenceBindingStatusState = "not_current"
)

// RestoreDrillEvidenceBindingStatus is a bounded read-only projection of whether
// a persisted restore-drill evidence binding still matches the exact current
// restore metadata and freshness assessment. It never grants restore authority.
type RestoreDrillEvidenceBindingStatus struct {
	SchemaVersion          string                                 `json:"schema_version"`
	State                  RestoreDrillEvidenceBindingStatusState `json:"state"`
	Reason                 string                                 `json:"reason"`
	BindingID              string                                 `json:"binding_id"`
	AssessmentID           string                                 `json:"assessment_id"`
	RestoreID              string                                 `json:"restore_id"`
	RestoreResourceVersion string                                 `json:"restore_resource_version"`
	CheckedAt              time.Time                              `json:"checked_at"`
	AdvisoryOnly           bool                                   `json:"advisory_only"`
	ProductionMutation     bool                                   `json:"production_mutation"`
}

// InspectRestoreDrillEvidenceBindingCurrent returns a stable, bounded status for
// operator/API consumption without exposing internal validation errors as an
// authorization signal. Invalid or drifted evidence is fail-closed as
// not_current. checkedAt is caller supplied so the result is deterministic.
func InspectRestoreDrillEvidenceBindingCurrent(
	restore RestoreMetadata,
	assessment RestoreDrillFreshnessAssessment,
	binding RestoreDrillEvidenceBinding,
	checkedAt time.Time,
) (RestoreDrillEvidenceBindingStatus, error) {
	if checkedAt.IsZero() {
		return RestoreDrillEvidenceBindingStatus{}, fmt.Errorf("restore drill evidence binding status: checked_at is required")
	}

	status := RestoreDrillEvidenceBindingStatus{
		SchemaVersion:          RestoreDrillEvidenceBindingStatusSchemaVersion,
		State:                  RestoreDrillEvidenceBindingNotCurrent,
		Reason:                 "binding_not_current",
		BindingID:              binding.BindingID,
		AssessmentID:           binding.AssessmentID,
		RestoreID:              binding.RestoreID,
		RestoreResourceVersion: binding.RestoreResourceVersion,
		CheckedAt:              checkedAt.UTC(),
		AdvisoryOnly:           true,
		ProductionMutation:     false,
	}

	if err := ValidateRestoreDrillEvidenceBindingCurrent(restore, assessment, binding); err != nil {
		return status, nil
	}

	status.State = RestoreDrillEvidenceBindingCurrent
	status.Reason = "binding_matches_exact_current_state"
	return status, nil
}
