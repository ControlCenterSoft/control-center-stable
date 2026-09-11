package recovery

import (
	"testing"
	"time"
)

func TestInspectRestoreDrillEvidenceBindingCurrentReportsCurrent(t *testing.T) {
	restore, assessment, binding := currentRestoreDrillEvidenceBindingFixture(t)
	checkedAt := assessment.CheckedAt.Add(time.Minute)

	status, err := InspectRestoreDrillEvidenceBindingCurrent(restore, assessment, binding, checkedAt)
	if err != nil {
		t.Fatalf("InspectRestoreDrillEvidenceBindingCurrent() error = %v", err)
	}
	if status.SchemaVersion != RestoreDrillEvidenceBindingStatusSchemaVersion {
		t.Fatalf("schema_version = %q", status.SchemaVersion)
	}
	if status.State != RestoreDrillEvidenceBindingCurrent {
		t.Fatalf("state = %q, want %q", status.State, RestoreDrillEvidenceBindingCurrent)
	}
	if status.Reason != "binding_matches_exact_current_state" {
		t.Fatalf("reason = %q", status.Reason)
	}
	if status.BindingID != binding.BindingID || status.AssessmentID != binding.AssessmentID {
		t.Fatal("status identity does not match binding")
	}
	if !status.CheckedAt.Equal(checkedAt.UTC()) {
		t.Fatalf("checked_at = %v, want %v", status.CheckedAt, checkedAt.UTC())
	}
	if !status.AdvisoryOnly || status.ProductionMutation {
		t.Fatal("status escaped advisory-only boundary")
	}
}

func TestInspectRestoreDrillEvidenceBindingCurrentFailsClosedOnDrift(t *testing.T) {
	restore, assessment, binding := currentRestoreDrillEvidenceBindingFixture(t)
	updated := restore
	updated.ResourceVersion += ":next"

	status, err := InspectRestoreDrillEvidenceBindingCurrent(updated, assessment, binding, assessment.CheckedAt)
	if err != nil {
		t.Fatalf("InspectRestoreDrillEvidenceBindingCurrent() error = %v", err)
	}
	if status.State != RestoreDrillEvidenceBindingNotCurrent {
		t.Fatalf("state = %q, want %q", status.State, RestoreDrillEvidenceBindingNotCurrent)
	}
	if status.Reason != "binding_not_current" {
		t.Fatalf("reason = %q", status.Reason)
	}
	if !status.AdvisoryOnly || status.ProductionMutation {
		t.Fatal("not-current status escaped advisory-only boundary")
	}
}

func TestInspectRestoreDrillEvidenceBindingCurrentRejectsMissingCheckedAt(t *testing.T) {
	restore, assessment, binding := currentRestoreDrillEvidenceBindingFixture(t)

	if _, err := InspectRestoreDrillEvidenceBindingCurrent(restore, assessment, binding, time.Time{}); err == nil {
		t.Fatal("missing checked_at accepted")
	}
}
