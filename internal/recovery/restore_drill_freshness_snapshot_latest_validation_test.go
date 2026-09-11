package recovery

import (
	"testing"
	"time"
)

func TestValidateRestoreDrillFreshnessSnapshotLatestCurrentAcceptsLatestEvidence(t *testing.T) {
	newest := validRestoreMetadata()
	setRestoreIdentity(&newest, "restore-drill-latest")
	older := validRestoreMetadata()
	setRestoreIdentity(&older, "restore-drill-older-latest-check")
	setRestoreVerifiedAt(&older, newest.Verification.VerifiedAt.Add(-2*time.Minute))

	checkedAt := newest.Verification.VerifiedAt.Add(time.Hour)
	snapshot, err := ReadLatestRestoreDrillFreshnessSnapshot(
		[]RestoreMetadata{older, newest}, newest.Target, 24*time.Hour, checkedAt,
	)
	if err != nil {
		t.Fatalf("ReadLatestRestoreDrillFreshnessSnapshot() error = %v", err)
	}

	if err := ValidateRestoreDrillFreshnessSnapshotLatestCurrent([]RestoreMetadata{older, newest}, snapshot); err != nil {
		t.Fatalf("ValidateRestoreDrillFreshnessSnapshotLatestCurrent() error = %v", err)
	}
}

func TestValidateRestoreDrillFreshnessSnapshotLatestCurrentRejectsNewerEvidence(t *testing.T) {
	older := validRestoreMetadata()
	setRestoreIdentity(&older, "restore-drill-before-new-evidence")
	checkedAt := older.Verification.VerifiedAt.Add(2 * time.Hour)
	snapshot, err := ReadLatestRestoreDrillFreshnessSnapshot(
		[]RestoreMetadata{older}, older.Target, 24*time.Hour, checkedAt,
	)
	if err != nil {
		t.Fatalf("ReadLatestRestoreDrillFreshnessSnapshot() error = %v", err)
	}

	newer := validRestoreMetadata()
	setRestoreIdentity(&newer, "restore-drill-after-snapshot")
	setRestoreVerifiedAt(&newer, older.Verification.VerifiedAt.Add(time.Minute))
	newer.CompletedAt = timePointer(newer.Verification.VerifiedAt.Add(time.Minute))
	newer.UpdatedAt = *newer.CompletedAt
	if err := ValidateRestoreMetadata(newer); err != nil {
		t.Fatalf("newer restore fixture is not valid metadata: %v", err)
	}

	if err := ValidateRestoreDrillFreshnessSnapshotLatestCurrent([]RestoreMetadata{older, newer}, snapshot); err == nil {
		t.Fatal("snapshot remained current after newer restore-drill evidence appeared")
	}
}

func TestValidateRestoreDrillFreshnessSnapshotLatestCurrentAcceptsAuthoritativeUnverifiableState(t *testing.T) {
	target := validRestoreMetadata().Target
	checkedAt := testNow.Add(48 * time.Hour)
	snapshot, err := ReadLatestRestoreDrillFreshnessSnapshot(nil, target, 24*time.Hour, checkedAt)
	if err != nil {
		t.Fatalf("ReadLatestRestoreDrillFreshnessSnapshot() error = %v", err)
	}

	if err := ValidateRestoreDrillFreshnessSnapshotLatestCurrent(nil, snapshot); err != nil {
		t.Fatalf("ValidateRestoreDrillFreshnessSnapshotLatestCurrent() error = %v", err)
	}
}

func TestValidateRestoreDrillFreshnessSnapshotLatestCurrentRejectsUnverifiableAfterEvidenceAppears(t *testing.T) {
	restore := validRestoreMetadata()
	checkedAt := restore.Verification.VerifiedAt.Add(time.Hour)
	snapshot, err := ReadLatestRestoreDrillFreshnessSnapshot(nil, restore.Target, 24*time.Hour, checkedAt)
	if err != nil {
		t.Fatalf("ReadLatestRestoreDrillFreshnessSnapshot() error = %v", err)
	}

	if err := ValidateRestoreDrillFreshnessSnapshotLatestCurrent([]RestoreMetadata{restore}, snapshot); err == nil {
		t.Fatal("unverifiable snapshot remained current after valid restore-drill evidence appeared")
	}
}
