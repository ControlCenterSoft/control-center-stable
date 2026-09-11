package recovery

import (
	"testing"
	"time"
)

func setRestoreIdentity(restore *RestoreMetadata, id string) {
	restore.ObjectID = id
	restore.ResourceVersion = "rv:" + id + ":1"
}

func setRestoreVerifiedAt(restore *RestoreMetadata, verifiedAt time.Time) {
	verifiedAt = verifiedAt.UTC().Truncate(time.Second)
	restore.Verification.VerifiedAt = &verifiedAt
	for i := range restore.Verification.Evidence {
		restore.Verification.Evidence[i].RecordedAt = verifiedAt
	}
}

func TestReadLatestRestoreDrillFreshnessSnapshotSelectsNewestEvidence(t *testing.T) {
	newest := validRestoreMetadata()
	setRestoreIdentity(&newest, "restore-drill-newest")
	older := validRestoreMetadata()
	setRestoreIdentity(&older, "restore-drill-older")
	setRestoreVerifiedAt(&older, newest.Verification.VerifiedAt.Add(-2*time.Minute))

	checkedAt := newest.Verification.VerifiedAt.Add(time.Hour)
	got, err := ReadLatestRestoreDrillFreshnessSnapshot(
		[]RestoreMetadata{newest, older}, newest.Target, 24*time.Hour, checkedAt,
	)
	if err != nil {
		t.Fatalf("ReadLatestRestoreDrillFreshnessSnapshot() error = %v", err)
	}
	if got.State != RestoreDrillSnapshotFresh || got.RestoreID != newest.ObjectID {
		t.Fatalf("newest verified drill not selected: %#v", got)
	}
	if got.AssessmentID == "" || got.VerificationEvidenceDigest == "" {
		t.Fatalf("snapshot lost exact evidence identity: %#v", got)
	}
	if !got.AdvisoryOnly || got.ProductionMutation {
		t.Fatalf("snapshot unexpectedly grants mutation authority: %#v", got)
	}
}

func TestReadLatestRestoreDrillFreshnessSnapshotPropagatesStale(t *testing.T) {
	restore := validRestoreMetadata()
	checkedAt := restore.Verification.VerifiedAt.Add(24 * time.Hour)
	got, err := ReadLatestRestoreDrillFreshnessSnapshot(
		[]RestoreMetadata{restore}, restore.Target, 24*time.Hour, checkedAt,
	)
	if err != nil {
		t.Fatalf("ReadLatestRestoreDrillFreshnessSnapshot() error = %v", err)
	}
	if got.State != RestoreDrillSnapshotStale {
		t.Fatalf("expected stale snapshot at freshness boundary: %#v", got)
	}
}

func TestReadLatestRestoreDrillFreshnessSnapshotFiltersExactTarget(t *testing.T) {
	wanted := validRestoreMetadata()
	setRestoreIdentity(&wanted, "restore-drill-wanted")
	other := validRestoreMetadata()
	setRestoreIdentity(&other, "restore-drill-other")
	other.Target.ObjectID = "database-other"
	setRestoreVerifiedAt(&other, wanted.Verification.VerifiedAt.Add(time.Minute))
	other.CompletedAt = timePointer(other.Verification.VerifiedAt.Add(time.Minute))
	other.UpdatedAt = *other.CompletedAt

	checkedAt := other.Verification.VerifiedAt.Add(time.Hour)
	got, err := ReadLatestRestoreDrillFreshnessSnapshot(
		[]RestoreMetadata{other, wanted}, wanted.Target, 24*time.Hour, checkedAt,
	)
	if err != nil {
		t.Fatalf("ReadLatestRestoreDrillFreshnessSnapshot() error = %v", err)
	}
	if got.RestoreID != wanted.ObjectID {
		t.Fatalf("snapshot selected evidence for another target: %#v", got)
	}
}

func TestReadLatestRestoreDrillFreshnessSnapshotTieBreaksByRestoreID(t *testing.T) {
	left := validRestoreMetadata()
	setRestoreIdentity(&left, "restore-drill-b")
	right := validRestoreMetadata()
	setRestoreIdentity(&right, "restore-drill-a")

	checkedAt := left.Verification.VerifiedAt.Add(time.Hour)
	got, err := ReadLatestRestoreDrillFreshnessSnapshot(
		[]RestoreMetadata{left, right}, left.Target, 24*time.Hour, checkedAt,
	)
	if err != nil {
		t.Fatalf("ReadLatestRestoreDrillFreshnessSnapshot() error = %v", err)
	}
	if got.RestoreID != "restore-drill-a" {
		t.Fatalf("tie break is not deterministic: %#v", got)
	}
}

func TestReadLatestRestoreDrillFreshnessSnapshotNoEvidenceFailsClosed(t *testing.T) {
	target := validRestoreMetadata().Target
	checkedAt := testNow.Add(48 * time.Hour)
	got, err := ReadLatestRestoreDrillFreshnessSnapshot(nil, target, 24*time.Hour, checkedAt)
	if err != nil {
		t.Fatalf("ReadLatestRestoreDrillFreshnessSnapshot() error = %v", err)
	}
	if got.State != RestoreDrillSnapshotUnverifiable || got.Reason == "" {
		t.Fatalf("missing evidence did not fail closed: %#v", got)
	}
	if got.RestoreID != "" || got.AssessmentID != "" || got.VerificationEvidenceDigest != "" {
		t.Fatalf("missing evidence fabricated lineage: %#v", got)
	}
}

func TestReadLatestRestoreDrillFreshnessSnapshotIgnoresNonDrillRestore(t *testing.T) {
	restore := validRestoreMetadata()
	restore.Mode = RestoreAlternate
	restore.Provider.Capabilities = []ProviderCapability{CapabilityRestore}
	restore.Verification.Evidence = []EvidenceReference{
		testEvidence("restore-proof", EvidenceChecksum, *restore.Verification.VerifiedAt),
	}

	checkedAt := restore.Verification.VerifiedAt.Add(time.Hour)
	got, err := ReadLatestRestoreDrillFreshnessSnapshot(
		[]RestoreMetadata{restore}, restore.Target, 24*time.Hour, checkedAt,
	)
	if err != nil {
		t.Fatalf("ReadLatestRestoreDrillFreshnessSnapshot() error = %v", err)
	}
	if got.State != RestoreDrillSnapshotUnverifiable {
		t.Fatalf("non-drill restore counted as drill freshness evidence: %#v", got)
	}
}

func TestReadLatestRestoreDrillFreshnessSnapshotRejectsInvalidInputs(t *testing.T) {
	target := validRestoreMetadata().Target
	checkedAt := testNow.Add(time.Hour)
	if _, err := ReadLatestRestoreDrillFreshnessSnapshot(nil, ObjectReference{}, time.Hour, checkedAt); err == nil {
		t.Fatal("invalid target accepted")
	}
	if _, err := ReadLatestRestoreDrillFreshnessSnapshot(nil, target, 0, checkedAt); err == nil {
		t.Fatal("invalid max age accepted")
	}
	if _, err := ReadLatestRestoreDrillFreshnessSnapshot(nil, target, time.Hour, time.Time{}); err == nil {
		t.Fatal("zero checked_at accepted")
	}
}
