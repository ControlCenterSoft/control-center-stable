package operationsview

import (
	"errors"
	"strings"
	"testing"
	"time"

	"control-center/internal/corecontracts"
	"control-center/internal/recovery"
)

var recoveryPathGraphTestNow = time.Date(2026, 9, 12, 3, 0, 0, 0, time.UTC)

func recoveryPathGraphMetadata(id string, createdAt, updatedAt time.Time) corecontracts.ObjectMetadata {
	return corecontracts.ObjectMetadata{
		ObjectID:        id,
		ScopeID:         "site-a",
		OwnerScope:      "site-a",
		Generation:      1,
		ResourceVersion: "rv:" + id + ":1",
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}
}

func recoveryPathGraphTarget() recovery.ObjectReference {
	return recovery.ObjectReference{
		Kind:         recovery.ObjectDatabase,
		ObjectID:     "database-primary",
		ScopeID:      "site-a",
		Statefulness: recovery.Stateful,
	}
}

func recoveryPathGraphEvidence(id string, kind recovery.EvidenceKind, recordedAt time.Time) recovery.EvidenceReference {
	return recovery.EvidenceReference{
		ID:         id,
		Kind:       kind,
		Reference:  "urn:cc:evidence:" + id,
		Digest:     "sha256:" + strings.Repeat("a", 64),
		RecordedAt: recordedAt,
	}
}

func validRecoveryPathGraph() recovery.RecoveryMetadataGraph {
	now := recoveryPathGraphTestNow
	target := recoveryPathGraphTarget()
	pointExpires := now.Add(24 * time.Hour)
	point := recovery.RecoveryPoint{
		ObjectMetadata: recoveryPathGraphMetadata("rp-31", now, now.Add(10*time.Minute)),
		SchemaVersion:  recovery.RecoveryPointSchemaVersion,
		State:          recovery.RecoveryPointReady,
		Trigger:        recovery.TriggerPreDestructive,
		Consistency:    recovery.ConsistencyApplication,
		Objects:        []recovery.ObjectReference{target},
		BackupIDs:      []string{"backup-31"},
		ChangeID:       "change-31",
		ExpiresAt:      &pointExpires,
	}

	requested := now.Add(time.Minute)
	started := now.Add(2 * time.Minute)
	captured := now.Add(5 * time.Minute)
	completed := now.Add(10 * time.Minute)
	verified := now.Add(39 * time.Minute)
	healthVerified := now.Add(40 * time.Minute)
	restoreEvidence := []recovery.EvidenceReference{
		recoveryPathGraphEvidence("restore-proof-31", recovery.EvidenceRestoreDrill, verified),
		recoveryPathGraphEvidence("functional-proof-31", recovery.EvidenceFunctionalTest, verified),
	}
	backup := recovery.BackupMetadata{
		ObjectMetadata:  recoveryPathGraphMetadata("backup-31", requested, healthVerified),
		SchemaVersion:   recovery.BackupMetadataSchemaVersion,
		RecoveryPointID: point.ObjectID,
		Target:          target,
		Mode:            recovery.BackupFull,
		State:           recovery.BackupCompleted,
		Provider: recovery.ProviderMetadata{
			ProviderID:   "pgbackrest",
			AdapterID:    "postgres.pgbackrest",
			Version:      "2.54.2",
			RepositoryID: "backup-repository-a",
			OperationID:  "provider-operation-31",
			Capabilities: []recovery.ProviderCapability{recovery.CapabilityBackup},
		},
		Artifact: &recovery.BackupArtifact{
			RepositoryID: "backup-repository-a",
			ObjectKey:    "site-a/database-primary/backup-31.tar.zst",
			SizeBytes:    4096,
			Digest:       "sha256:" + strings.Repeat("b", 64),
			Encryption:   recovery.EncryptionProviderManaged,
		},
		RequestedAt:    requested,
		StartedAt:      &started,
		DataCapturedAt: &captured,
		CompletedAt:    &completed,
		Health: recovery.BackupHealth{
			State:          recovery.BackupHealthVerified,
			LastVerifiedAt: &healthVerified,
			RestoreID:      "restore-drill-31",
			Evidence:       append([]recovery.EvidenceReference(nil), restoreEvidence...),
		},
	}

	restoreRequested := now.Add(30 * time.Minute)
	restoreStarted := now.Add(31 * time.Minute)
	restoreCompleted := now.Add(40 * time.Minute)
	restore := recovery.RestoreMetadata{
		ObjectMetadata:   recoveryPathGraphMetadata("restore-drill-31", restoreRequested, restoreCompleted),
		SchemaVersion:    recovery.RestoreMetadataSchemaVersion,
		RecoveryPointID:  point.ObjectID,
		BackupIDs:        []string{backup.ObjectID},
		Mode:             recovery.RestoreIsolatedDrill,
		State:            recovery.RestoreSucceeded,
		Target:           target,
		RequestedObjects: []recovery.ObjectReference{},
		Provider: recovery.ProviderMetadata{
			ProviderID:   "pgbackrest",
			AdapterID:    "postgres.pgbackrest",
			Version:      "2.54.2",
			RepositoryID: "backup-repository-a",
			OperationID:  "restore-operation-31",
			Capabilities: []recovery.ProviderCapability{recovery.CapabilityRestore, recovery.CapabilityRestoreDrill},
		},
		Fencing: recovery.FencingMetadata{
			State:     recovery.FencingNotRequired,
			TargetIDs: []string{},
			Evidence:  []recovery.EvidenceReference{},
		},
		RequestedAt: restoreRequested,
		StartedAt:   &restoreStarted,
		CompletedAt: &restoreCompleted,
		Verification: recovery.RestoreVerification{
			Outcome:    recovery.VerificationPassed,
			VerifiedAt: &verified,
			Evidence:   append([]recovery.EvidenceReference(nil), restoreEvidence...),
		},
	}

	return recovery.RecoveryMetadataGraph{
		RecoveryPoints:    []recovery.RecoveryPoint{point},
		Backups:           []recovery.BackupMetadata{backup},
		Restores:          []recovery.RestoreMetadata{restore},
		ObjectiveEvidence: []recovery.RecoveryObjectiveEvidence{},
	}
}

func TestBuildRecoveryPathEvidenceFromGraphReady(t *testing.T) {
	graph := validRecoveryPathGraph()
	now := recoveryPathGraphTestNow.Add(45 * time.Minute)

	evidence, err := BuildRecoveryPathEvidenceFromGraph(RecoveryPathGraphInput{
		ChangeID:           "change-31",
		RevisionID:         "revision-31",
		RevisionDigest:     recoveryPathTestDigest,
		RecoveryPointID:    "rp-31",
		Graph:              graph,
		VerificationMaxAge: 24 * time.Hour,
		EvaluatedAt:        now,
	})
	if err != nil {
		t.Fatalf("BuildRecoveryPathEvidenceFromGraph() error = %v", err)
	}
	if evidence.State != RecoveryPathReady || evidence.BlockReason != RecoveryBlockNone {
		t.Fatalf("state=%q reason=%q, want ready", evidence.State, evidence.BlockReason)
	}
	if evidence.BackupCount != 1 || evidence.VerifiedBackupCount != 1 || evidence.VerificationOutcome != recovery.VerificationPassed {
		t.Fatalf("unexpected derived evidence: %#v", evidence)
	}
	if evidence.VerificationObservedAt == nil || !evidence.VerificationObservedAt.Equal(recoveryPathGraphTestNow.Add(40*time.Minute)) {
		t.Fatalf("unexpected verification timestamp: %#v", evidence.VerificationObservedAt)
	}
}

func TestBuildRecoveryPathEvidenceFromGraphExpiresStaleVerification(t *testing.T) {
	graph := validRecoveryPathGraph()
	now := recoveryPathGraphTestNow.Add(2 * time.Hour)

	evidence, err := BuildRecoveryPathEvidenceFromGraph(RecoveryPathGraphInput{
		ChangeID:           "change-31",
		RevisionID:         "revision-31",
		RevisionDigest:     recoveryPathTestDigest,
		RecoveryPointID:    "rp-31",
		Graph:              graph,
		VerificationMaxAge: 30 * time.Minute,
		EvaluatedAt:        now,
	})
	if err != nil {
		t.Fatalf("BuildRecoveryPathEvidenceFromGraph() error = %v", err)
	}
	if evidence.State != RecoveryPathExpired || evidence.BlockReason != RecoveryBlockEvidenceExpired {
		t.Fatalf("state=%q reason=%q, want expired evidence", evidence.State, evidence.BlockReason)
	}
}

func TestBuildRecoveryPathEvidenceFromGraphRejectsWrongChangeAndIncompleteGraph(t *testing.T) {
	graph := validRecoveryPathGraph()
	now := recoveryPathGraphTestNow.Add(45 * time.Minute)
	input := RecoveryPathGraphInput{
		ChangeID:           "change-31",
		RevisionID:         "revision-31",
		RevisionDigest:     recoveryPathTestDigest,
		RecoveryPointID:    "rp-31",
		Graph:              graph,
		VerificationMaxAge: 24 * time.Hour,
		EvaluatedAt:        now,
	}

	wrongChange := input
	wrongChange.Graph = validRecoveryPathGraph()
	wrongChange.Graph.RecoveryPoints[0].ChangeID = "change-other"
	if _, err := BuildRecoveryPathEvidenceFromGraph(wrongChange); !errors.Is(err, ErrInvalidRecoveryPathEvidence) {
		t.Fatalf("wrong Change binding error = %v, want ErrInvalidRecoveryPathEvidence", err)
	}

	incomplete := input
	incomplete.Graph = validRecoveryPathGraph()
	incomplete.Graph.Restores = nil
	if _, err := BuildRecoveryPathEvidenceFromGraph(incomplete); !errors.Is(err, ErrInvalidRecoveryPathEvidence) {
		t.Fatalf("incomplete graph error = %v, want ErrInvalidRecoveryPathEvidence", err)
	}
}
