package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"control-center/internal/orchestration/change"
	"control-center/internal/orchestration/operationsview"
)

func TestApplyVerifiedBlastRadiusEvidenceMarksOnlyExactRevisionAvailable(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 20, 0, 0, time.UTC)
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
	blastRadius, err := operationsview.BuildBlastRadiusEvidence(operationsview.BlastRadiusInput{
		ChangeID:       "change-a",
		RevisionID:     "revision-a",
		RevisionDigest: digest,
		ObservedAt:     now.Add(-time.Minute),
		Resources: []operationsview.BlastRadiusResource{
			{ResourceID: "service-a", Kind: "service", Relation: operationsview.BlastRadiusDirect, ReasonCode: "change.target"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	enriched, err := ApplyVerifiedBlastRadiusEvidence(view, map[string]string{"revision-a": digest}, []operationsview.BlastRadiusEvidence{blastRadius}, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if enriched.Changes[0].BlastRadius != EvidenceAvailable {
		t.Fatalf("validated evidence was not projected: %#v", enriched.Changes[0])
	}
	if view.Changes[0].BlastRadius != EvidenceUnavailable {
		t.Fatalf("source view was mutated: %#v", view.Changes[0])
	}
}

func TestApplyVerifiedBlastRadiusEvidenceRejectsDigestMismatchWithoutPartialView(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 20, 0, 0, time.UTC)
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
	blastRadius, err := operationsview.BuildBlastRadiusEvidence(operationsview.BlastRadiusInput{
		ChangeID: "change-a", RevisionID: "revision-a", RevisionDigest: other,
		ObservedAt: now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyVerifiedBlastRadiusEvidence(view, map[string]string{"revision-a": trusted}, []operationsview.BlastRadiusEvidence{blastRadius}, true, now)
	if !errors.Is(err, ErrInvalidChangesJobsView) {
		t.Fatalf("expected fail-closed digest mismatch, got %v", err)
	}
	if result.ContractVersion != "" || len(result.Changes) != 0 {
		t.Fatalf("invalid evidence returned a partial view: %#v", result)
	}
}

func TestApplyVerifiedBlastRadiusEvidenceRejectsFutureEvidence(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 20, 0, 0, time.UTC)
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
	blastRadius, err := operationsview.BuildBlastRadiusEvidence(operationsview.BlastRadiusInput{
		ChangeID: "change-a", RevisionID: "revision-a", RevisionDigest: digest,
		ObservedAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyVerifiedBlastRadiusEvidence(view, map[string]string{"revision-a": digest}, []operationsview.BlastRadiusEvidence{blastRadius}, true, now); !errors.Is(err, ErrInvalidChangesJobsView) {
		t.Fatalf("future evidence must fail closed, got %v", err)
	}
}

func TestApplyVerifiedBlastRadiusEvidenceKeepsUnavailableWhenSourceNotLoaded(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 20, 0, 0, time.UTC)
	view, err := BuildChangesJobsView(ChangesJobsInput{
		Changes:       []change.Snapshot{validChangeSnapshot("change-a", "service.ensure", now.Add(-10*time.Minute))},
		ChangesLoaded: true,
		JobsLoaded:    true,
		Now:           now,
	})
	if err != nil {
		t.Fatal(err)
	}
	view.Changes[0].BlastRadius = EvidenceAvailable
	closed, err := ApplyVerifiedBlastRadiusEvidence(view, nil, nil, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Changes[0].BlastRadius != EvidenceUnavailable {
		t.Fatalf("unloaded typed source must fail closed: %#v", closed.Changes[0])
	}
}
