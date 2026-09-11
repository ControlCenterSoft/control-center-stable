package recovery

import (
	"testing"
	"time"
)

func validRestoreDrillFreshnessSnapshotForValidation(t *testing.T) RestoreDrillFreshnessSnapshot {
	t.Helper()
	restore := validRestoreMetadata()
	checkedAt := restore.Verification.VerifiedAt.Add(time.Hour)
	snapshot, err := ReadLatestRestoreDrillFreshnessSnapshot(
		[]RestoreMetadata{restore}, restore.Target, 24*time.Hour, checkedAt,
	)
	if err != nil {
		t.Fatalf("ReadLatestRestoreDrillFreshnessSnapshot() error = %v", err)
	}
	return snapshot
}

func TestValidateRestoreDrillFreshnessSnapshotAcceptsGeneratedFreshSnapshot(t *testing.T) {
	snapshot := validRestoreDrillFreshnessSnapshotForValidation(t)
	if err := ValidateRestoreDrillFreshnessSnapshot(snapshot); err != nil {
		t.Fatalf("ValidateRestoreDrillFreshnessSnapshot() error = %v", err)
	}
}

func TestValidateRestoreDrillFreshnessSnapshotAcceptsGeneratedUnverifiableSnapshot(t *testing.T) {
	target := validRestoreMetadata().Target
	snapshot, err := ReadLatestRestoreDrillFreshnessSnapshot(nil, target, 24*time.Hour, testNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("ReadLatestRestoreDrillFreshnessSnapshot() error = %v", err)
	}
	if err := ValidateRestoreDrillFreshnessSnapshot(snapshot); err != nil {
		t.Fatalf("ValidateRestoreDrillFreshnessSnapshot() error = %v", err)
	}
}

func TestValidateRestoreDrillFreshnessSnapshotRejectsTampering(t *testing.T) {
	tests := []struct {
		name string
		edit func(*RestoreDrillFreshnessSnapshot)
	}{
		{
			name: "mutation authority",
			edit: func(snapshot *RestoreDrillFreshnessSnapshot) {
				snapshot.ProductionMutation = true
			},
		},
		{
			name: "malformed evidence digest",
			edit: func(snapshot *RestoreDrillFreshnessSnapshot) {
				snapshot.VerificationEvidenceDigest = "sha256:deadbeef"
			},
		},
		{
			name: "canonical-looking assessment id substitution",
			edit: func(snapshot *RestoreDrillFreshnessSnapshot) {
				snapshot.AssessmentID = "rdf-aaaaaaaaaaaaaaaaaaaaaaaa"
			},
		},
		{
			name: "freshness state mismatch",
			edit: func(snapshot *RestoreDrillFreshnessSnapshot) {
				snapshot.State = RestoreDrillSnapshotStale
			},
		},
		{
			name: "valid until drift",
			edit: func(snapshot *RestoreDrillFreshnessSnapshot) {
				value := snapshot.ValidUntil.Add(time.Second)
				snapshot.ValidUntil = &value
			},
		},
		{
			name: "unexpected reason on verified state",
			edit: func(snapshot *RestoreDrillFreshnessSnapshot) {
				snapshot.Reason = "should-not-be-present"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := validRestoreDrillFreshnessSnapshotForValidation(t)
			test.edit(&snapshot)
			if err := ValidateRestoreDrillFreshnessSnapshot(snapshot); err == nil {
				t.Fatal("tampered snapshot accepted")
			}
		})
	}
}

func TestValidateRestoreDrillFreshnessSnapshotRejectsFabricatedUnverifiableLineage(t *testing.T) {
	target := validRestoreMetadata().Target
	snapshot, err := ReadLatestRestoreDrillFreshnessSnapshot(nil, target, 24*time.Hour, testNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("ReadLatestRestoreDrillFreshnessSnapshot() error = %v", err)
	}
	snapshot.RestoreID = "fabricated-restore"
	if err := ValidateRestoreDrillFreshnessSnapshot(snapshot); err == nil {
		t.Fatal("unverifiable snapshot with fabricated lineage accepted")
	}
}

func TestValidateRestoreDrillFreshnessSnapshotRejectsSubsecondCheckedAt(t *testing.T) {
	snapshot := validRestoreDrillFreshnessSnapshotForValidation(t)
	snapshot.CheckedAt = snapshot.CheckedAt.Add(time.Nanosecond)
	if err := ValidateRestoreDrillFreshnessSnapshot(snapshot); err == nil {
		t.Fatal("subsecond checked_at accepted")
	}
}
