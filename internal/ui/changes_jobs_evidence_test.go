package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"control-center/internal/orchestration/change"
)

func TestApplyChangeEvidenceBindsExactImmutableRevision(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 40, 0, 0, time.UTC)
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
	enriched, err := ApplyChangeEvidence(view, map[string]string{"revision-a": digest}, []ChangeEvidenceSnapshot{{
		ChangeID:          "change-a",
		RevisionID:        "revision-a",
		RevisionDigest:    digest,
		SemanticDiff:      EvidenceAvailable,
		BlastRadius:       EvidenceAvailable,
		MaintenanceWindow: EvidenceUnavailable,
		RecoveryEvidence:  EvidenceAvailable,
		ObservedAt:        now.Add(-time.Minute),
	}}, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := enriched.Changes[0]; got.SemanticDiff != EvidenceAvailable || got.BlastRadius != EvidenceAvailable || got.MaintenanceWindow != EvidenceUnavailable || got.RecoveryEvidence != EvidenceAvailable {
		t.Fatalf("unexpected evidence projection: %#v", got)
	}
	if original := view.Changes[0]; original.SemanticDiff != EvidenceUnavailable || original.BlastRadius != EvidenceUnavailable || original.RecoveryEvidence != EvidenceUnavailable {
		t.Fatalf("source view was mutated: %#v", original)
	}
	if err := ValidateChangesJobsView(enriched); err != nil {
		t.Fatalf("enriched view validation: %v", err)
	}
}

func TestApplyChangeEvidenceRejectsRevisionDigestMismatchWithoutPartialView(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 40, 0, 0, time.UTC)
	view, err := BuildChangesJobsView(ChangesJobsInput{
		Changes: []change.Snapshot{
			validChangeSnapshot("change-a", "service.ensure", now.Add(-10*time.Minute)),
			validChangeSnapshot("change-b", "service.ensure", now.Add(-9*time.Minute)),
		},
		ChangesLoaded: true,
		JobsLoaded:    true,
		Now:           now,
	})
	if err != nil {
		t.Fatal(err)
	}
	trusted := "sha256:" + strings.Repeat("a", 64)
	mismatched := "sha256:" + strings.Repeat("b", 64)
	enriched, err := ApplyChangeEvidence(view, map[string]string{"revision-a": trusted}, []ChangeEvidenceSnapshot{
		{
			ChangeID:       "change-a",
			RevisionID:     "revision-a",
			RevisionDigest: trusted,
			SemanticDiff:   EvidenceAvailable,
			BlastRadius:    EvidenceAvailable,
			ObservedAt:     now.Add(-time.Minute),
		},
		{
			ChangeID:       "change-b",
			RevisionID:     "revision-a",
			RevisionDigest: mismatched,
			SemanticDiff:   EvidenceAvailable,
			BlastRadius:    EvidenceAvailable,
			ObservedAt:     now.Add(-time.Minute),
		},
	}, true, now)
	if !errors.Is(err, ErrInvalidChangesJobsView) {
		t.Fatalf("err=%v", err)
	}
	if enriched.ContractVersion != "" || len(enriched.Changes) != 0 {
		t.Fatalf("invalid evidence returned a partial view: %#v", enriched)
	}
}

func TestApplyChangeEvidenceRejectsRevisionIDMismatch(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 40, 0, 0, time.UTC)
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
	_, err = ApplyChangeEvidence(view, map[string]string{"revision-b": digest}, []ChangeEvidenceSnapshot{{
		ChangeID:       "change-a",
		RevisionID:     "revision-b",
		RevisionDigest: digest,
		SemanticDiff:   EvidenceAvailable,
		BlastRadius:    EvidenceAvailable,
		ObservedAt:     now.Add(-time.Minute),
	}}, true, now)
	if !errors.Is(err, ErrInvalidChangesJobsView) {
		t.Fatalf("err=%v", err)
	}
}

func TestApplyChangeEvidenceRejectsFutureAndNonCanonicalEvidence(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 40, 0, 0, time.UTC)
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
	cases := []ChangeEvidenceSnapshot{
		{
			ChangeID:       "change-a",
			RevisionID:     "revision-a",
			RevisionDigest: digest,
			SemanticDiff:   EvidenceAvailable,
			BlastRadius:    EvidenceAvailable,
			ObservedAt:     now.Add(time.Minute),
		},
		{
			ChangeID:       " change-a",
			RevisionID:     "revision-a",
			RevisionDigest: digest,
			SemanticDiff:   EvidenceAvailable,
			BlastRadius:    EvidenceAvailable,
			ObservedAt:     now.Add(-time.Minute),
		},
		{
			ChangeID:       "change-a",
			RevisionID:     "revision-a",
			RevisionDigest: "sha256:" + strings.Repeat("A", 64),
			SemanticDiff:   EvidenceAvailable,
			BlastRadius:    EvidenceAvailable,
			ObservedAt:     now.Add(-time.Minute),
		},
	}
	for _, snapshot := range cases {
		if _, err := ApplyChangeEvidence(view, map[string]string{"revision-a": digest}, []ChangeEvidenceSnapshot{snapshot}, true, now); !errors.Is(err, ErrInvalidChangesJobsView) {
			t.Fatalf("snapshot=%#v err=%v", snapshot, err)
		}
	}
}

func TestApplyChangeEvidenceKeepsViewFailClosedWhenSourceNotLoaded(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 40, 0, 0, time.UTC)
	view, err := BuildChangesJobsView(ChangesJobsInput{
		Changes:       []change.Snapshot{validChangeSnapshot("change-a", "service.ensure", now.Add(-10*time.Minute))},
		ChangesLoaded: true,
		JobsLoaded:    true,
		Now:           now,
	})
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := ApplyChangeEvidence(view, nil, nil, false, now)
	if err != nil {
		t.Fatal(err)
	}
	got := unchanged.Changes[0]
	if got.SemanticDiff != EvidenceUnavailable || got.BlastRadius != EvidenceUnavailable || got.MaintenanceWindow != EvidenceUnavailable || got.RecoveryEvidence != EvidenceUnavailable {
		t.Fatalf("unloaded evidence source was invented: %#v", got)
	}

	digest := "sha256:" + strings.Repeat("a", 64)
	_, err = ApplyChangeEvidence(view, map[string]string{"revision-a": digest}, []ChangeEvidenceSnapshot{{
		ChangeID:       "change-a",
		RevisionID:     "revision-a",
		RevisionDigest: digest,
		SemanticDiff:   EvidenceAvailable,
		ObservedAt:     now,
	}}, false, now)
	if !errors.Is(err, ErrInvalidChangesJobsView) {
		t.Fatalf("snapshots with unloaded source must fail closed: %v", err)
	}
}
