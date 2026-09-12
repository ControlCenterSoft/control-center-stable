package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"control-center/internal/orchestration/change"
	"control-center/internal/orchestration/operationsview"
)

func TestApplyVerifiedMaintenanceWindowEvidenceMarksExactRevisionAvailable(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 45, 0, 0, time.UTC)
	view, err := BuildChangesJobsView(ChangesJobsInput{
		Changes:       []change.Snapshot{validChangeSnapshot("change-a", "service.ensure", now.Add(-10*time.Minute))},
		ChangesLoaded: true,
		JobsLoaded:    true,
		Now:           now,
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	window := operationsview.MaintenanceWindow{StartsAt: now.Add(time.Hour), EndsAt: now.Add(2 * time.Hour)}
	maintenance, err := operationsview.BuildMaintenanceWindowEvidence(
		"change-a", "revision-a", digest, true, &window, now.Add(-time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}

	enriched, err := ApplyVerifiedMaintenanceWindowEvidence(
		view,
		map[string]string{"revision-a": digest},
		[]operationsview.MaintenanceWindowEvidence{maintenance},
		true,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if enriched.Changes[0].MaintenanceWindow != EvidenceAvailable {
		t.Fatalf("validated maintenance evidence was not projected: %#v", enriched.Changes[0])
	}
	if view.Changes[0].MaintenanceWindow != EvidenceUnavailable {
		t.Fatalf("source view was mutated: %#v", view.Changes[0])
	}
}

func TestApplyVerifiedMaintenanceWindowEvidenceRejectsDigestMismatchWithoutPartialView(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 45, 0, 0, time.UTC)
	view, err := BuildChangesJobsView(ChangesJobsInput{
		Changes:       []change.Snapshot{validChangeSnapshot("change-a", "service.ensure", now.Add(-10*time.Minute))},
		ChangesLoaded: true,
		JobsLoaded:    true,
		Now:           now,
	})
	if err != nil {
		t.Fatal(err)
	}
	trusted := "sha256:" + strings.Repeat("a", 64)
	other := "sha256:" + strings.Repeat("b", 64)
	maintenance, err := operationsview.BuildMaintenanceWindowEvidence(
		"change-a", "revision-a", other, false, nil, now.Add(-time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := ApplyVerifiedMaintenanceWindowEvidence(
		view,
		map[string]string{"revision-a": trusted},
		[]operationsview.MaintenanceWindowEvidence{maintenance},
		true,
		now,
	)
	if !errors.Is(err, ErrInvalidChangesJobsView) {
		t.Fatalf("expected fail-closed digest mismatch, got %v", err)
	}
	if result.ContractVersion != "" || len(result.Changes) != 0 {
		t.Fatalf("invalid evidence returned a partial view: %#v", result)
	}
}

func TestApplyVerifiedMaintenanceWindowEvidenceRejectsFutureAndAuthorizingEvidence(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 45, 0, 0, time.UTC)
	view, err := BuildChangesJobsView(ChangesJobsInput{
		Changes:       []change.Snapshot{validChangeSnapshot("change-a", "service.ensure", now.Add(-10*time.Minute))},
		ChangesLoaded: true,
		JobsLoaded:    true,
		Now:           now,
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("c", 64)
	future, err := operationsview.BuildMaintenanceWindowEvidence(
		"change-a", "revision-a", digest, false, nil, now.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyVerifiedMaintenanceWindowEvidence(view, map[string]string{"revision-a": digest}, []operationsview.MaintenanceWindowEvidence{future}, true, now); !errors.Is(err, ErrInvalidChangesJobsView) {
		t.Fatalf("future evidence must fail closed, got %v", err)
	}

	authorizing, err := operationsview.BuildMaintenanceWindowEvidence(
		"change-a", "revision-a", digest, false, nil, now.Add(-time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	authorizing.ExecutionAuthorized = true
	if _, err := ApplyVerifiedMaintenanceWindowEvidence(view, map[string]string{"revision-a": digest}, []operationsview.MaintenanceWindowEvidence{authorizing}, true, now); !errors.Is(err, ErrInvalidChangesJobsView) {
		t.Fatalf("authorizing maintenance evidence must fail closed, got %v", err)
	}
}

func TestApplyVerifiedMaintenanceWindowEvidenceKeepsUnavailableWhenSourceNotLoaded(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 45, 0, 0, time.UTC)
	view, err := BuildChangesJobsView(ChangesJobsInput{
		Changes:       []change.Snapshot{validChangeSnapshot("change-a", "service.ensure", now.Add(-10*time.Minute))},
		ChangesLoaded: true,
		JobsLoaded:    true,
		Now:           now,
	})
	if err != nil {
		t.Fatal(err)
	}
	view.Changes[0].MaintenanceWindow = EvidenceAvailable
	closed, err := ApplyVerifiedMaintenanceWindowEvidence(view, nil, nil, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Changes[0].MaintenanceWindow != EvidenceUnavailable {
		t.Fatalf("unloaded typed source must fail closed: %#v", closed.Changes[0])
	}
}
