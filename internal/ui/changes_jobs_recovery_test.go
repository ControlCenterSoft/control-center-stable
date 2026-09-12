package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"control-center/internal/orchestration/change"
	"control-center/internal/orchestration/operationsview"
	"control-center/internal/recovery"
)

func TestApplyVerifiedRecoveryPathEvidenceMarksExactRevisionAvailable(t *testing.T) {
	now := time.Date(2026, 9, 12, 3, 30, 0, 0, time.UTC)
	view, err := BuildChangesJobsView(ChangesJobsInput{
		Changes:       []change.Snapshot{validChangeSnapshot("change-a", "service.ensure", now.Add(-10*time.Minute))},
		ChangesLoaded: true,
		JobsLoaded:    true,
		Now:           now,
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("d", 64)
	verifiedAt := now.Add(-time.Minute)
	recoveryEvidence, err := operationsview.BuildRecoveryPathEvidence(
		"change-a", "revision-a", digest,
		operationsview.RecoveryPathObservation{
			RecoveryPointID: "rp-a", RecoveryPointState: recovery.RecoveryPointReady,
			BackupCount: 1, VerifiedBackupCount: 1, VerificationOutcome: recovery.VerificationPassed,
			VerificationObservedAt: &verifiedAt,
		},
		now.Add(-30*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}

	enriched, err := ApplyVerifiedRecoveryPathEvidence(
		view,
		map[string]string{"revision-a": digest},
		[]operationsview.RecoveryPathEvidence{recoveryEvidence},
		true,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if enriched.Changes[0].RecoveryEvidence != EvidenceAvailable {
		t.Fatalf("validated recovery evidence was not projected: %#v", enriched.Changes[0])
	}
	if view.Changes[0].RecoveryEvidence != EvidenceUnavailable {
		t.Fatalf("source view was mutated: %#v", view.Changes[0])
	}
}

func TestApplyVerifiedRecoveryPathEvidenceRejectsMismatchAndAuthority(t *testing.T) {
	now := time.Date(2026, 9, 12, 3, 30, 0, 0, time.UTC)
	view, err := BuildChangesJobsView(ChangesJobsInput{
		Changes:       []change.Snapshot{validChangeSnapshot("change-a", "service.ensure", now.Add(-10*time.Minute))},
		ChangesLoaded: true,
		JobsLoaded:    true,
		Now:           now,
	})
	if err != nil {
		t.Fatal(err)
	}
	trusted := "sha256:" + strings.Repeat("d", 64)
	other := "sha256:" + strings.Repeat("e", 64)
	verifiedAt := now.Add(-time.Minute)
	recoveryEvidence, err := operationsview.BuildRecoveryPathEvidence(
		"change-a", "revision-a", other,
		operationsview.RecoveryPathObservation{
			RecoveryPointID: "rp-a", RecoveryPointState: recovery.RecoveryPointReady,
			BackupCount: 1, VerifiedBackupCount: 1, VerificationOutcome: recovery.VerificationPassed,
			VerificationObservedAt: &verifiedAt,
		},
		now.Add(-30*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := ApplyVerifiedRecoveryPathEvidence(view, map[string]string{"revision-a": trusted}, []operationsview.RecoveryPathEvidence{recoveryEvidence}, true, now); !errors.Is(err, ErrInvalidChangesJobsView) {
		t.Fatalf("digest mismatch must fail closed, got %v", err)
	}

	recoveryEvidence.RevisionDigest = trusted
	recoveryEvidence.ExecutionAuthorized = true
	if _, err := ApplyVerifiedRecoveryPathEvidence(view, map[string]string{"revision-a": trusted}, []operationsview.RecoveryPathEvidence{recoveryEvidence}, true, now); !errors.Is(err, ErrInvalidChangesJobsView) {
		t.Fatalf("authorizing recovery evidence must fail closed, got %v", err)
	}
}

func TestApplyVerifiedRecoveryPathEvidenceKeepsUnavailableWhenSourceNotLoaded(t *testing.T) {
	now := time.Date(2026, 9, 12, 3, 30, 0, 0, time.UTC)
	view, err := BuildChangesJobsView(ChangesJobsInput{
		Changes:       []change.Snapshot{validChangeSnapshot("change-a", "service.ensure", now.Add(-10*time.Minute))},
		ChangesLoaded: true,
		JobsLoaded:    true,
		Now:           now,
	})
	if err != nil {
		t.Fatal(err)
	}
	view.Changes[0].RecoveryEvidence = EvidenceAvailable
	closed, err := ApplyVerifiedRecoveryPathEvidence(view, nil, nil, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Changes[0].RecoveryEvidence != EvidenceUnavailable {
		t.Fatalf("unloaded typed source must fail closed: %#v", closed.Changes[0])
	}
}
