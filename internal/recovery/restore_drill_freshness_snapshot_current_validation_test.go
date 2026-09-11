package recovery

import (
	"strings"
	"testing"
	"time"
)

func currentRestoreDrillFreshnessSnapshotFixture(t *testing.T) (RestoreMetadata, RestoreDrillFreshnessSnapshot) {
	t.Helper()

	restore := validRestoreMetadata()
	checkedAt := restore.Verification.VerifiedAt.Add(time.Hour)
	snapshot, err := ReadLatestRestoreDrillFreshnessSnapshot(
		[]RestoreMetadata{restore}, restore.Target, 24*time.Hour, checkedAt,
	)
	if err != nil {
		t.Fatalf("ReadLatestRestoreDrillFreshnessSnapshot() error = %v", err)
	}
	if snapshot.State != RestoreDrillSnapshotFresh {
		t.Fatalf("fixture snapshot state = %q, want %q", snapshot.State, RestoreDrillSnapshotFresh)
	}
	return restore, snapshot
}

func TestValidateRestoreDrillFreshnessSnapshotCurrentAcceptsExactCurrentEvidence(t *testing.T) {
	restore, snapshot := currentRestoreDrillFreshnessSnapshotFixture(t)

	if err := ValidateRestoreDrillFreshnessSnapshotCurrent(restore, snapshot); err != nil {
		t.Fatalf("ValidateRestoreDrillFreshnessSnapshotCurrent() error = %v", err)
	}
}

func TestValidateRestoreDrillFreshnessSnapshotCurrentRejectsResourceVersionDrift(t *testing.T) {
	restore, snapshot := currentRestoreDrillFreshnessSnapshotFixture(t)
	restore.ResourceVersion += ":next"

	if err := ValidateRestoreDrillFreshnessSnapshotCurrent(restore, snapshot); err == nil {
		t.Fatal("resource version drift accepted")
	}
}

func TestValidateRestoreDrillFreshnessSnapshotCurrentRejectsGenerationDrift(t *testing.T) {
	restore, snapshot := currentRestoreDrillFreshnessSnapshotFixture(t)
	restore.Generation++

	if err := ValidateRestoreDrillFreshnessSnapshotCurrent(restore, snapshot); err == nil {
		t.Fatal("restore generation drift accepted")
	}
}

func TestValidateRestoreDrillFreshnessSnapshotCurrentRejectsVerificationEvidenceDrift(t *testing.T) {
	restore, snapshot := currentRestoreDrillFreshnessSnapshotFixture(t)
	restore.Verification.Evidence[0].Digest = "sha256:" + strings.Repeat("b", 64)

	if err := ValidateRestoreMetadata(restore); err != nil {
		t.Fatalf("drifted restore fixture is not valid metadata: %v", err)
	}
	if err := ValidateRestoreDrillFreshnessSnapshotCurrent(restore, snapshot); err == nil {
		t.Fatal("verification evidence drift accepted")
	}
}

func TestValidateRestoreDrillFreshnessSnapshotCurrentRejectsUnverifiableSnapshot(t *testing.T) {
	restore := validRestoreMetadata()
	checkedAt := restore.Verification.VerifiedAt.Add(time.Hour)
	snapshot, err := ReadLatestRestoreDrillFreshnessSnapshot(
		nil, restore.Target, 24*time.Hour, checkedAt,
	)
	if err != nil {
		t.Fatalf("ReadLatestRestoreDrillFreshnessSnapshot() error = %v", err)
	}
	if snapshot.State != RestoreDrillSnapshotUnverifiable {
		t.Fatalf("fixture snapshot state = %q, want %q", snapshot.State, RestoreDrillSnapshotUnverifiable)
	}

	if err := ValidateRestoreDrillFreshnessSnapshotCurrent(restore, snapshot); err == nil {
		t.Fatal("unverifiable snapshot accepted as exact-current evidence")
	}
}
