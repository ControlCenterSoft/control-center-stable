package recovery

import (
	"strings"
	"testing"
	"time"
)

func backupProvider(capabilities ...ProviderCapability) ProviderMetadata {
	return ProviderMetadata{
		ProviderID: "pgbackrest", AdapterID: "postgres.pgbackrest", Version: "2.54.2",
		RepositoryID: "backup-repository-a", OperationID: "provider-operation-001", Capabilities: capabilities,
	}
}

func validBackupMetadata() BackupMetadata {
	requested := testNow.Add(-time.Minute)
	started := testNow
	captured := testNow.Add(5 * time.Minute)
	completed := testNow.Add(10 * time.Minute)
	return BackupMetadata{
		ObjectMetadata:  testObjectMetadata("backup-001", requested, completed),
		SchemaVersion:   BackupMetadataSchemaVersion,
		RecoveryPointID: "rp-20260908-001",
		Target:          testObject(ObjectDatabase, Stateful), Mode: BackupFull, State: BackupCompleted,
		Provider: backupProvider(CapabilityBackup),
		Artifact: &BackupArtifact{
			RepositoryID: "backup-repository-a", ObjectKey: "site-a/database-primary/backup-001.tar.zst",
			SizeBytes: 4096, Digest: "sha256:" + strings.Repeat("b", 64), Encryption: EncryptionProviderManaged,
		},
		RequestedAt: requested,
		StartedAt:   &started, DataCapturedAt: &captured, CompletedAt: &completed,
		Health: BackupHealth{State: BackupHealthUnverified, Evidence: []EvidenceReference{}},
	}
}

func TestValidateBackupMetadataCompletedAndVerified(t *testing.T) {
	backup := validBackupMetadata()
	if err := ValidateBackupMetadata(backup); err != nil {
		t.Fatalf("ValidateBackupMetadata() error = %v", err)
	}
	verifiedAt := testNow.Add(2 * time.Hour)
	backup.UpdatedAt = verifiedAt
	backup.Health = BackupHealth{
		State: BackupHealthVerified, LastVerifiedAt: &verifiedAt, RestoreID: "restore-drill-001",
		Evidence: []EvidenceReference{
			testEvidence("restore-proof", EvidenceRestoreDrill, verifiedAt),
			testEvidence("functional-proof", EvidenceFunctionalTest, verifiedAt),
		},
	}
	if err := ValidateBackupMetadata(backup); err != nil {
		t.Fatalf("verified backup rejected: %v", err)
	}
	backup.Health = BackupHealth{
		State: BackupHealthDegraded, LastVerifiedAt: &verifiedAt, RestoreID: "restore-drill-002",
		Evidence: []EvidenceReference{
			testEvidence("degraded-restore-proof", EvidenceRestoreDrill, verifiedAt),
		},
	}
	if err := ValidateBackupMetadata(backup); err != nil {
		t.Fatalf("degraded backup health rejected: %v", err)
	}
}

func TestValidateBackupMetadataBaseWAL(t *testing.T) {
	backup := validBackupMetadata()
	backup.Mode = BackupBaseWAL
	backup.Provider.Capabilities = []ProviderCapability{CapabilityBackup, CapabilityBaseBackup, CapabilityWALArchive, CapabilityPITR}
	backup.PITR = &PITRWindow{
		Timeline: 1, StartLSN: "0/1000000", EndLSN: "0/2000000",
		EarliestRestoreAt: testNow.Add(5 * time.Minute), LatestRestoreAt: testNow.Add(9 * time.Minute),
	}
	if err := ValidateBackupMetadata(backup); err != nil {
		t.Fatalf("BASE_WAL backup rejected: %v", err)
	}
}

func TestValidateBackupMetadataLifecycleStates(t *testing.T) {
	planned := validBackupMetadata()
	planned.State = BackupPlanned
	planned.Provider.OperationID = ""
	planned.Artifact, planned.StartedAt, planned.DataCapturedAt, planned.CompletedAt = nil, nil, nil, nil
	planned.Health = BackupHealth{State: BackupHealthUnknown, Evidence: []EvidenceReference{}}
	if err := ValidateBackupMetadata(planned); err != nil {
		t.Fatalf("planned backup rejected: %v", err)
	}
	running := planned
	running.State = BackupRunning
	running.Provider.OperationID = "provider-operation-001"
	running.StartedAt = timePointer(testNow)
	if err := ValidateBackupMetadata(running); err != nil {
		t.Fatalf("running backup rejected: %v", err)
	}
	failed := running
	failed.State = BackupFailed
	failed.CompletedAt = timePointer(testNow.Add(time.Minute))
	failed.Failure = testFailure()
	failed.Health = BackupHealth{State: BackupHealthFailed, Evidence: []EvidenceReference{}}
	if err := ValidateBackupMetadata(failed); err != nil {
		t.Fatalf("failed backup metadata rejected: %v", err)
	}
}

func TestValidateBackupMetadataRejectsInvalidContracts(t *testing.T) {
	tests := []struct {
		name string
		edit func(*BackupMetadata)
	}{
		{name: "schema", edit: func(v *BackupMetadata) { v.SchemaVersion = "backup/v0" }},
		{name: "recovery point", edit: func(v *BackupMetadata) { v.RecoveryPointID = "" }},
		{name: "target", edit: func(v *BackupMetadata) { v.Target.ObjectID = "" }},
		{name: "cross-scope target", edit: func(v *BackupMetadata) { v.Target.ScopeID = "site-b" }},
		{name: "mode", edit: func(v *BackupMetadata) { v.Mode = "COPY" }},
		{name: "state", edit: func(v *BackupMetadata) { v.State = "SUCCESS" }},
		{name: "provider id", edit: func(v *BackupMetadata) { v.Provider.ProviderID = "" }},
		{name: "provider version", edit: func(v *BackupMetadata) { v.Provider.Version = "latest" }},
		{name: "provider repository", edit: func(v *BackupMetadata) { v.Provider.RepositoryID = "" }},
		{name: "provider operation", edit: func(v *BackupMetadata) { v.Provider.OperationID = "" }},
		{name: "provider missing backup", edit: func(v *BackupMetadata) { v.Provider.Capabilities = []ProviderCapability{CapabilityRestore} }},
		{name: "provider duplicate capability", edit: func(v *BackupMetadata) {
			v.Provider.Capabilities = []ProviderCapability{CapabilityBackup, CapabilityBackup}
		}},
		{name: "requested timestamp", edit: func(v *BackupMetadata) { v.RequestedAt = time.Time{} }},
		{name: "request after metadata update", edit: func(v *BackupMetadata) { v.RequestedAt = v.UpdatedAt.Add(time.Second) }},
		{name: "start before request", edit: func(v *BackupMetadata) { v.StartedAt = timePointer(v.RequestedAt.Add(-time.Second)) }},
		{name: "incremental missing parent", edit: func(v *BackupMetadata) { v.Mode = BackupIncremental }},
		{name: "parent on full", edit: func(v *BackupMetadata) { v.ParentBackupID = "backup-parent" }},
		{name: "self parent", edit: func(v *BackupMetadata) { v.Mode = BackupDifferential; v.ParentBackupID = v.ObjectID }},
		{name: "artifact repository mismatch", edit: func(v *BackupMetadata) { v.Artifact.RepositoryID = "other-repository" }},
		{name: "artifact absolute path", edit: func(v *BackupMetadata) { v.Artifact.ObjectKey = "/etc/passwd" }},
		{name: "artifact traversal", edit: func(v *BackupMetadata) { v.Artifact.ObjectKey = "site-a/../secret" }},
		{name: "artifact trailing traversal", edit: func(v *BackupMetadata) { v.Artifact.ObjectKey = "site-a/.." }},
		{name: "artifact current directory", edit: func(v *BackupMetadata) { v.Artifact.ObjectKey = "site-a/./backup.tar" }},
		{name: "artifact duplicate separator", edit: func(v *BackupMetadata) { v.Artifact.ObjectKey = "site-a//backup.tar" }},
		{name: "artifact backslash", edit: func(v *BackupMetadata) { v.Artifact.ObjectKey = `site-a\backup.tar` }},
		{name: "artifact surrounding whitespace", edit: func(v *BackupMetadata) { v.Artifact.ObjectKey = " site-a/backup.tar" }},
		{name: "artifact size", edit: func(v *BackupMetadata) { v.Artifact.SizeBytes = 0 }},
		{name: "artifact digest", edit: func(v *BackupMetadata) { v.Artifact.Digest = "sha256:bad" }},
		{name: "encryption", edit: func(v *BackupMetadata) { v.Artifact.Encryption = "CUSTOM" }},
		{name: "customer encryption key missing", edit: func(v *BackupMetadata) { v.Artifact.Encryption = EncryptionCustomerManaged }},
		{name: "provider encryption key unexpected", edit: func(v *BackupMetadata) { v.Artifact.EncryptionKeyID = "key-1" }},
		{name: "timestamp reversal", edit: func(v *BackupMetadata) { v.CompletedAt = timePointer(testNow.Add(-time.Second)) }},
		{name: "capture before start", edit: func(v *BackupMetadata) { v.DataCapturedAt = timePointer(testNow.Add(-time.Second)) }},
		{name: "capture without start", edit: func(v *BackupMetadata) { v.StartedAt = nil }},
		{name: "completed no artifact", edit: func(v *BackupMetadata) { v.Artifact = nil }},
		{name: "completion after metadata update", edit: func(v *BackupMetadata) { v.UpdatedAt = v.CompletedAt.Add(-time.Second) }},
		{name: "completed has failure", edit: func(v *BackupMetadata) { v.Failure = testFailure() }},
		{name: "unknown completed health", edit: func(v *BackupMetadata) { v.Health.State = BackupHealthUnknown }},
		{name: "null health evidence", edit: func(v *BackupMetadata) { v.Health.Evidence = nil }},
		{name: "health verification before completion", edit: func(v *BackupMetadata) {
			at := v.CompletedAt.Add(-time.Second)
			v.Health = BackupHealth{
				State: BackupHealthVerified, LastVerifiedAt: &at, RestoreID: "restore-1",
				Evidence: []EvidenceReference{
					testEvidence("restore-proof", EvidenceRestoreDrill, at),
					testEvidence("functional-proof", EvidenceFunctionalTest, at),
				},
			}
		}},
		{name: "health evidence before completion", edit: func(v *BackupMetadata) {
			at := v.CompletedAt.Add(time.Minute)
			v.Health = BackupHealth{
				State: BackupHealthVerified, LastVerifiedAt: &at, RestoreID: "restore-1",
				Evidence: []EvidenceReference{
					testEvidence("restore-proof", EvidenceRestoreDrill, v.CompletedAt.Add(-time.Second)),
					testEvidence("functional-proof", EvidenceFunctionalTest, at),
				},
			}
		}},
		{name: "verified no drill", edit: func(v *BackupMetadata) {
			at := testNow.Add(time.Hour)
			v.Health = BackupHealth{State: BackupHealthVerified, LastVerifiedAt: &at, RestoreID: "restore-1", Evidence: []EvidenceReference{testEvidence("checksum", EvidenceChecksum, at)}}
		}},
		{name: "verified evidence after timestamp", edit: func(v *BackupMetadata) {
			at := testNow.Add(time.Hour)
			v.Health = BackupHealth{
				State: BackupHealthVerified, LastVerifiedAt: &at, RestoreID: "restore-1",
				Evidence: []EvidenceReference{
					testEvidence("restore-proof", EvidenceRestoreDrill, at),
					testEvidence("functional-proof", EvidenceFunctionalTest, at.Add(time.Second)),
				},
			}
		}},
		{name: "degraded missing restore", edit: func(v *BackupMetadata) {
			at := testNow.Add(time.Hour)
			v.Health = BackupHealth{State: BackupHealthDegraded, LastVerifiedAt: &at, Evidence: []EvidenceReference{testEvidence("restore-proof", EvidenceRestoreDrill, at)}}
		}},
		{name: "degraded missing drill", edit: func(v *BackupMetadata) {
			at := testNow.Add(time.Hour)
			v.Health = BackupHealth{State: BackupHealthDegraded, LastVerifiedAt: &at, RestoreID: "restore-1", Evidence: []EvidenceReference{testEvidence("checksum", EvidenceChecksum, at)}}
		}},
		{name: "degraded evidence after timestamp", edit: func(v *BackupMetadata) {
			at := testNow.Add(time.Hour)
			v.Health = BackupHealth{State: BackupHealthDegraded, LastVerifiedAt: &at, RestoreID: "restore-1", Evidence: []EvidenceReference{testEvidence("restore-proof", EvidenceRestoreDrill, at.Add(time.Second))}}
		}},
		{name: "unverified with evidence", edit: func(v *BackupMetadata) {
			v.Health.Evidence = []EvidenceReference{testEvidence("checksum", EvidenceChecksum, testNow)}
		}},
		{name: "base wal missing pitr", edit: func(v *BackupMetadata) {
			v.Mode = BackupBaseWAL
			v.Provider.Capabilities = []ProviderCapability{CapabilityBackup, CapabilityBaseBackup, CapabilityWALArchive, CapabilityPITR}
		}},
		{name: "pitr on full", edit: func(v *BackupMetadata) {
			v.PITR = &PITRWindow{Timeline: 1, StartLSN: "0/1", EndLSN: "0/2", EarliestRestoreAt: testNow, LatestRestoreAt: testNow}
		}},
		{name: "base wal missing capability", edit: func(v *BackupMetadata) {
			v.Mode = BackupBaseWAL
			v.PITR = &PITRWindow{Timeline: 1, StartLSN: "0/1", EndLSN: "0/2", EarliestRestoreAt: testNow, LatestRestoreAt: testNow}
		}},
		{name: "pitr reversed lsn", edit: func(v *BackupMetadata) {
			v.Mode = BackupBaseWAL
			v.Provider.Capabilities = []ProviderCapability{CapabilityBackup, CapabilityBaseBackup, CapabilityWALArchive, CapabilityPITR}
			v.PITR = &PITRWindow{Timeline: 1, StartLSN: "0/20", EndLSN: "0/10", EarliestRestoreAt: testNow, LatestRestoreAt: testNow}
		}},
		{name: "snapshot missing capability", edit: func(v *BackupMetadata) { v.Mode = BackupSnapshot }},
		{name: "object version missing capability", edit: func(v *BackupMetadata) { v.Mode = BackupObjectVersion }},
		{name: "expired drops capture timestamp", edit: func(v *BackupMetadata) { v.State = BackupExpired; v.DataCapturedAt = nil }},
		{name: "deleted retains failure", edit: func(v *BackupMetadata) { v.State = BackupDeleted; v.Artifact = nil; v.Failure = testFailure() }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validBackupMetadata()
			test.edit(&value)
			if err := ValidateBackupMetadata(value); err == nil {
				t.Fatal("invalid backup metadata accepted")
			}
		})
	}
}
