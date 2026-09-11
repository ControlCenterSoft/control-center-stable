package recovery

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func validRecoveryMetadataGraph() RecoveryMetadataGraph {
	point := validRecoveryPoint()
	point.CreatedAt = testNow
	point.UpdatedAt = testNow.Add(10 * time.Minute)
	expires := testNow.Add(24 * time.Hour)
	point.ExpiresAt = &expires

	backup := validBackupMetadata()
	requested := testNow.Add(time.Minute)
	started := testNow.Add(2 * time.Minute)
	captured := testNow.Add(5 * time.Minute)
	completed := testNow.Add(10 * time.Minute)
	verified := testNow.Add(39 * time.Minute)
	healthVerified := testNow.Add(40 * time.Minute)
	backup.ObjectMetadata = testObjectMetadata("backup-001", requested, healthVerified)
	backup.RequestedAt = requested
	backup.StartedAt = &started
	backup.DataCapturedAt = &captured
	backup.CompletedAt = &completed

	restore := validRestoreMetadata()
	restoreRequested := testNow.Add(30 * time.Minute)
	restoreStarted := testNow.Add(31 * time.Minute)
	restoreCompleted := testNow.Add(40 * time.Minute)
	restore.ObjectMetadata = testObjectMetadata("restore-drill-001", restoreRequested, restoreCompleted)
	restore.RequestedAt = restoreRequested
	restore.StartedAt = &restoreStarted
	restore.CompletedAt = &restoreCompleted
	restore.Verification.VerifiedAt = &verified
	restore.Verification.Evidence = []EvidenceReference{
		testEvidence("restore-proof", EvidenceRestoreDrill, verified),
		testEvidence("functional-proof", EvidenceFunctionalTest, verified),
	}

	backup.Health = BackupHealth{
		State:          BackupHealthVerified,
		LastVerifiedAt: &healthVerified,
		RestoreID:      restore.ObjectID,
		Evidence:       append([]EvidenceReference(nil), restore.Verification.Evidence...),
	}

	measured := testNow.Add(41 * time.Minute)
	objective := validObjectiveEvidence()
	objective.ObjectMetadata = testObjectMetadata("objective-evidence-001", measured, measured)
	objective.MeasuredAt = measured
	objective.ObservedRTOSeconds = uint64Pointer(9 * 60)
	objective.Evidence = append([]EvidenceReference(nil), restore.Verification.Evidence...)

	return RecoveryMetadataGraph{
		RecoveryPoints:    []RecoveryPoint{point},
		Backups:           []BackupMetadata{backup},
		Restores:          []RestoreMetadata{restore},
		ObjectiveEvidence: []RecoveryObjectiveEvidence{objective},
	}
}

func validIncrementalRecoveryMetadataGraph() RecoveryMetadataGraph {
	graph := validRecoveryMetadataGraph()
	graph.Backups[0].Mode = BackupIncremental
	graph.Backups[0].ParentBackupID = "backup-parent"

	parentPoint := validRecoveryPoint()
	parentPoint.ObjectMetadata = testObjectMetadata("rp-parent", testNow.Add(-2*time.Hour), testNow.Add(-100*time.Minute))
	parentPoint.BackupIDs = []string{"backup-parent"}
	parentPoint.ChangeID = "change-parent"
	expires := testNow.Add(22 * time.Hour)
	parentPoint.ExpiresAt = &expires

	parentRequested := testNow.Add(-110 * time.Minute)
	parentStarted := testNow.Add(-109 * time.Minute)
	parentCaptured := testNow.Add(-105 * time.Minute)
	parentCompleted := testNow.Add(-100 * time.Minute)
	parent := validBackupMetadata()
	parent.ObjectMetadata = testObjectMetadata("backup-parent", parentRequested, parentCompleted)
	parent.RecoveryPointID = parentPoint.ObjectID
	parent.Provider.OperationID = "provider-operation-parent"
	parent.Artifact.ObjectKey = "site-a/database-primary/backup-parent.tar.zst"
	parent.RequestedAt = parentRequested
	parent.StartedAt = &parentStarted
	parent.DataCapturedAt = &parentCaptured
	parent.CompletedAt = &parentCompleted

	graph.RecoveryPoints = append(graph.RecoveryPoints, parentPoint)
	graph.Backups = append(graph.Backups, parent)
	return graph
}

func TestValidateRecoveryMetadataGraph(t *testing.T) {
	if err := ValidateRecoveryMetadataGraph(validRecoveryMetadataGraph()); err != nil {
		t.Fatalf("ValidateRecoveryMetadataGraph() error = %v", err)
	}
	if err := ValidateRecoveryMetadataGraph(validIncrementalRecoveryMetadataGraph()); err != nil {
		t.Fatalf("incremental graph rejected: %v", err)
	}
	empty := RecoveryMetadataGraph{
		RecoveryPoints:    []RecoveryPoint{},
		Backups:           []BackupMetadata{},
		Restores:          []RestoreMetadata{},
		ObjectiveEvidence: []RecoveryObjectiveEvidence{},
	}
	if err := ValidateRecoveryMetadataGraph(empty); err != nil {
		t.Fatalf("complete empty graph rejected: %v", err)
	}
}

func TestValidateRecoveryMetadataGraphRejectsUnavailableCollections(t *testing.T) {
	tests := []struct {
		name string
		edit func(*RecoveryMetadataGraph)
	}{
		{name: "recovery points", edit: func(v *RecoveryMetadataGraph) { v.RecoveryPoints = nil }},
		{name: "backups", edit: func(v *RecoveryMetadataGraph) { v.Backups = nil }},
		{name: "restores", edit: func(v *RecoveryMetadataGraph) { v.Restores = nil }},
		{name: "objective evidence", edit: func(v *RecoveryMetadataGraph) { v.ObjectiveEvidence = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph := validRecoveryMetadataGraph()
			test.edit(&graph)
			assertIntegrityRejected(t, graph, "complete non-nil snapshot")
		})
	}
}

func TestValidateRecoveryMetadataGraphRejectsRecordAndRecoveryPointCorruption(t *testing.T) {
	tests := []struct {
		name string
		want string
		edit func(*RecoveryMetadataGraph)
	}{
		{name: "invalid nested record", want: "recovery_points[0].schema_version", edit: func(v *RecoveryMetadataGraph) { v.RecoveryPoints[0].SchemaVersion = "recovery.point/v0" }},
		{name: "duplicate same type id", want: "duplicates recovery object id", edit: func(v *RecoveryMetadataGraph) { v.RecoveryPoints = append(v.RecoveryPoints, v.RecoveryPoints[0]) }},
		{name: "cross type id collision", want: "duplicates recovery object id", edit: func(v *RecoveryMetadataGraph) { v.Restores[0].ObjectID = v.Backups[0].ObjectID }},
		{name: "missing backup", want: "referenced backup does not exist", edit: func(v *RecoveryMetadataGraph) { v.RecoveryPoints[0].BackupIDs[0] = "backup-missing" }},
		{name: "inverse point mismatch", want: "different recovery point", edit: func(v *RecoveryMetadataGraph) { v.Backups[0].RecoveryPointID = "rp-other" }},
		{name: "backup scope mismatch", want: "different scope_id", edit: func(v *RecoveryMetadataGraph) {
			v.Backups[0].ScopeID = "site-b"
			v.Backups[0].Target.ScopeID = "site-b"
		}},
		{name: "backup owner mismatch", want: "different owner_scope", edit: func(v *RecoveryMetadataGraph) { v.Backups[0].OwnerScope = "management-a" }},
		{name: "backup target outside point", want: "outside the recovery point", edit: func(v *RecoveryMetadataGraph) { v.Backups[0].Target.ObjectID = "database-shadow" }},
		{name: "ready object uncovered", want: "no completed backup", edit: func(v *RecoveryMetadataGraph) {
			v.RecoveryPoints[0].Objects = append(v.RecoveryPoints[0].Objects, ObjectReference{Kind: ObjectConfiguration, ObjectID: "config-a", ScopeID: "site-a", Statefulness: Stateful})
		}},
		{name: "active backup listed", want: "backup state is not coherent", edit: func(v *RecoveryMetadataGraph) {
			backup := &v.Backups[0]
			backup.State = BackupRunning
			backup.Artifact = nil
			backup.DataCapturedAt = nil
			backup.CompletedAt = nil
			backup.Health = BackupHealth{State: BackupHealthUnknown, Evidence: []EvidenceReference{}}
		}},
		{name: "successful backup orphaned", want: "not referenced by its recovery point", edit: func(v *RecoveryMetadataGraph) {
			backup := v.Backups[0]
			backup.ObjectID = "backup-002"
			backup.ResourceVersion = "rv:backup-002:1"
			backup.Provider.OperationID = "provider-operation-002"
			artifact := *backup.Artifact
			artifact.ObjectKey = "site-a/database-primary/backup-002.tar.zst"
			backup.Artifact = &artifact
			backup.Health = BackupHealth{State: BackupHealthUnverified, Evidence: []EvidenceReference{}}
			backup.UpdatedAt = *backup.CompletedAt
			v.Backups = append(v.Backups, backup)
		}},
		{name: "backup predates point", want: "predates recovery point", edit: func(v *RecoveryMetadataGraph) { v.RecoveryPoints[0].CreatedAt = testNow.Add(90 * time.Second) }},
		{name: "point published before backup completion", want: "completion follows recovery point", edit: func(v *RecoveryMetadataGraph) { v.RecoveryPoints[0].UpdatedAt = testNow.Add(9 * time.Minute) }},
		{name: "duplicate artifact claim", want: "artifact is already claimed", edit: func(v *RecoveryMetadataGraph) {
			backup := v.Backups[0]
			backup.ObjectID = "backup-002"
			backup.ResourceVersion = "rv:backup-002:1"
			backup.Provider.OperationID = "provider-operation-002"
			backup.Health = BackupHealth{State: BackupHealthUnverified, Evidence: []EvidenceReference{}}
			backup.UpdatedAt = *backup.CompletedAt
			v.Backups = append(v.Backups, backup)
			v.RecoveryPoints[0].BackupIDs = append(v.RecoveryPoints[0].BackupIDs, backup.ObjectID)
		}},
		{name: "duplicate provider operation", want: "provider operation is already claimed", edit: func(v *RecoveryMetadataGraph) { v.Restores[0].Provider.OperationID = v.Backups[0].Provider.OperationID }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph := validRecoveryMetadataGraph()
			test.edit(&graph)
			assertIntegrityRejected(t, graph, test.want)
		})
	}
}

func TestValidateRecoveryMetadataGraphRejectsParentChainCorruption(t *testing.T) {
	tests := []struct {
		name string
		want string
		edit func(*RecoveryMetadataGraph)
	}{
		{name: "missing parent", want: "parent backup does not exist", edit: func(v *RecoveryMetadataGraph) { v.Backups[0].ParentBackupID = "backup-missing" }},
		{name: "cycle", want: "parent cycle", edit: func(v *RecoveryMetadataGraph) {
			v.Backups[1].Mode = BackupIncremental
			v.Backups[1].ParentBackupID = v.Backups[0].ObjectID
		}},
		{name: "different target", want: "different target", edit: func(v *RecoveryMetadataGraph) {
			v.Backups[1].Target.ObjectID = "database-old"
			v.RecoveryPoints[1].Objects[0] = v.Backups[1].Target
		}},
		{name: "different provider", want: "different provider or repository", edit: func(v *RecoveryMetadataGraph) { v.Backups[1].Provider.ProviderID = "other-provider" }},
		{name: "parent unavailable", want: "not an available completed artifact", edit: func(v *RecoveryMetadataGraph) {
			v.Backups[1].State = BackupDeleted
			v.Backups[1].Artifact = nil
			v.RecoveryPoints[1].State = RecoveryPointExpired
			expires := v.RecoveryPoints[1].UpdatedAt.Add(-time.Second)
			v.RecoveryPoints[1].ExpiresAt = &expires
		}},
		{name: "parent completed too late", want: "completed after child request", edit: func(v *RecoveryMetadataGraph) {
			late := v.Backups[0].RequestedAt.Add(time.Second)
			v.Backups[1].CompletedAt = &late
			v.Backups[1].UpdatedAt = late
			v.RecoveryPoints[1].UpdatedAt = late
		}},
		{name: "differential non-full parent", want: "requires a FULL parent", edit: func(v *RecoveryMetadataGraph) {
			v.Backups[0].Mode = BackupDifferential
			v.Backups[1].Mode = BackupSnapshot
			v.Backups[1].Provider.Capabilities = append(v.Backups[1].Provider.Capabilities, CapabilitySnapshot)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph := validIncrementalRecoveryMetadataGraph()
			test.edit(&graph)
			assertIntegrityRejected(t, graph, test.want)
		})
	}
}

func TestValidateRecoveryMetadataGraphRejectsRestoreCorruption(t *testing.T) {
	tests := []struct {
		name string
		want string
		edit func(*RecoveryMetadataGraph)
	}{
		{name: "missing point", want: "recovery point does not exist", edit: func(v *RecoveryMetadataGraph) { v.Restores[0].RecoveryPointID = "rp-missing" }},
		{name: "missing backup", want: "referenced backup does not exist", edit: func(v *RecoveryMetadataGraph) { v.Restores[0].BackupIDs[0] = "backup-missing" }},
		{name: "restore scope mismatch", want: "different scope_id", edit: func(v *RecoveryMetadataGraph) {
			v.Restores[0].ScopeID = "site-b"
			v.Restores[0].Target.ScopeID = "site-b"
		}},
		{name: "restore owner mismatch", want: "different owner_scope", edit: func(v *RecoveryMetadataGraph) { v.Restores[0].OwnerScope = "management-a" }},
		{name: "deleted source", want: "no available completed artifact", edit: func(v *RecoveryMetadataGraph) {
			point := &v.RecoveryPoints[0]
			point.State = RecoveryPointExpired
			expires := testNow.Add(20 * time.Hour)
			point.ExpiresAt = &expires
			point.UpdatedAt = testNow.Add(25 * time.Hour)
			backup := &v.Backups[0]
			backup.State = BackupDeleted
			backup.Artifact = nil
		}},
		{name: "provider mismatch", want: "provider does not match", edit: func(v *RecoveryMetadataGraph) { v.Restores[0].Provider.ProviderID = "restore-proxy" }},
		{name: "request before backup complete", want: "predates backup completion", edit: func(v *RecoveryMetadataGraph) {
			v.Restores[0].RequestedAt = testNow.Add(9 * time.Minute)
			v.Restores[0].CreatedAt = v.Restores[0].RequestedAt
		}},
		{name: "drill target type mismatch", want: "target type is not compatible", edit: func(v *RecoveryMetadataGraph) {
			v.Restores[0].Target.Kind = ObjectService
			v.Restores[0].Target.Statefulness = Stateless
		}},
		{name: "object outside point", want: "requested object is outside", edit: func(v *RecoveryMetadataGraph) {
			restore := &v.Restores[0]
			restore.Mode = RestoreObjectLevel
			restore.Provider.Capabilities = []ProviderCapability{CapabilityRestore, CapabilityObjectRestore}
			restore.RequestedObjects = []ObjectReference{{Kind: ObjectUser, ObjectID: "user-alice", ScopeID: "site-a", Statefulness: Stateless}}
			restore.Verification.Evidence = []EvidenceReference{testEvidence("object-proof", EvidenceChecksum, *restore.Verification.VerifiedAt)}
			v.Backups[0].Health = BackupHealth{State: BackupHealthUnverified, Evidence: []EvidenceReference{}}
			v.ObjectiveEvidence = []RecoveryObjectiveEvidence{}
		}},
		{name: "object not covered by restore sources", want: "not covered by referenced backups", edit: func(v *RecoveryMetadataGraph) {
			requested := ObjectReference{Kind: ObjectUser, ObjectID: "user-alice", ScopeID: "site-a", Statefulness: Stateless}
			point := &v.RecoveryPoints[0]
			point.State = RecoveryPointPartial
			point.Objects = append(point.Objects, requested)
			point.Failure = testFailure()
			restore := &v.Restores[0]
			restore.Mode = RestoreObjectLevel
			restore.Provider.Capabilities = []ProviderCapability{CapabilityRestore, CapabilityObjectRestore}
			restore.RequestedObjects = []ObjectReference{requested}
			restore.Verification.Evidence = []EvidenceReference{testEvidence("object-proof", EvidenceChecksum, *restore.Verification.VerifiedAt)}
			v.Backups[0].Health = BackupHealth{State: BackupHealthUnverified, Evidence: []EvidenceReference{}}
			v.ObjectiveEvidence = []RecoveryObjectiveEvidence{}
		}},
		{name: "pitr without source window", want: "no referenced BASE_WAL backup", edit: func(v *RecoveryMetadataGraph) {
			restore := &v.Restores[0]
			restore.Mode = RestorePITR
			restore.Provider.Capabilities = []ProviderCapability{CapabilityRestore, CapabilityPITR}
			target := testNow.Add(4 * time.Minute)
			restore.PITRTargetAt = &target
			confirmed := restore.RequestedAt.Add(30 * time.Second)
			restore.Fencing = confirmedFencing()
			restore.Fencing.ConfirmedAt = &confirmed
			restore.Fencing.Evidence[0].RecordedAt = confirmed
			restore.Verification.Evidence = []EvidenceReference{testEvidence("pitr-proof", EvidenceChecksum, *restore.Verification.VerifiedAt)}
			v.Backups[0].Health = BackupHealth{State: BackupHealthUnverified, Evidence: []EvidenceReference{}}
			v.ObjectiveEvidence = []RecoveryObjectiveEvidence{}
		}},
		{name: "evidence predates start", want: "predates restore start", edit: func(v *RecoveryMetadataGraph) {
			when := v.Restores[0].RequestedAt
			for index := range v.Restores[0].Verification.Evidence {
				v.Restores[0].Verification.Evidence[index].RecordedAt = when
				v.Backups[0].Health.Evidence[index].RecordedAt = when
				v.ObjectiveEvidence[0].Evidence[index].RecordedAt = when
			}
		}},
		{name: "ambiguous evidence origin", want: "evidence id is already claimed", edit: func(v *RecoveryMetadataGraph) {
			restore := v.Restores[0]
			restore.ObjectID = "restore-drill-002"
			restore.ResourceVersion = "rv:restore-drill-002:1"
			restore.Provider.OperationID = "restore-operation-002"
			v.Restores = append(v.Restores, restore)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph := validRecoveryMetadataGraph()
			test.edit(&graph)
			assertIntegrityRejected(t, graph, test.want)
		})
	}
}

func TestValidateRecoveryMetadataGraphRejectsEvidenceProvenanceCorruption(t *testing.T) {
	tests := []struct {
		name string
		want string
		edit func(*RecoveryMetadataGraph)
	}{
		{name: "health restore missing", want: "referenced restore does not exist", edit: func(v *RecoveryMetadataGraph) { v.Backups[0].Health.RestoreID = "restore-missing" }},
		{name: "health evidence modified", want: "differs from its immutable origin", edit: func(v *RecoveryMetadataGraph) {
			v.Backups[0].Health.Evidence[0].Digest = "sha256:" + strings.Repeat("c", 64)
		}},
		{name: "verified from failed restore", want: "VERIFIED requires a successful", edit: func(v *RecoveryMetadataGraph) {
			v.Restores[0].State = RestoreFailed
			v.Restores[0].Verification.Outcome = VerificationFailed
			v.Restores[0].Failure = testFailure()
		}},
		{name: "degraded from successful restore", want: "DEGRADED requires a failed", edit: func(v *RecoveryMetadataGraph) {
			v.Backups[0].Health.State = BackupHealthDegraded
			v.Backups[0].Health.Evidence = v.Backups[0].Health.Evidence[:1]
		}},
		{name: "objective restore missing", want: "referenced restore does not exist", edit: func(v *RecoveryMetadataGraph) {
			v.Backups[0].Health = BackupHealth{State: BackupHealthUnverified, Evidence: []EvidenceReference{}}
			v.ObjectiveEvidence[0].RestoreID = "restore-missing"
		}},
		{name: "objective target outside graph", want: "target is not covered", edit: func(v *RecoveryMetadataGraph) {
			v.Backups[0].Health = BackupHealth{State: BackupHealthUnverified, Evidence: []EvidenceReference{}}
			v.ObjectiveEvidence[0].Target.ObjectID = "database-shadow"
		}},
		{name: "objective evidence unknown", want: "has no restore provenance", edit: func(v *RecoveryMetadataGraph) {
			v.Backups[0].Health = BackupHealth{State: BackupHealthUnverified, Evidence: []EvidenceReference{}}
			v.ObjectiveEvidence[0].Evidence[0].ID = "unknown-proof"
			v.ObjectiveEvidence[0].Evidence[0].Reference = "urn:cc:evidence:unknown-proof"
		}},
		{name: "objective evidence modified", want: "differs from its immutable origin", edit: func(v *RecoveryMetadataGraph) {
			v.Backups[0].Health = BackupHealth{State: BackupHealthUnverified, Evidence: []EvidenceReference{}}
			v.ObjectiveEvidence[0].Evidence[0].Digest = "sha256:" + strings.Repeat("c", 64)
		}},
		{name: "measurement before restore completion", want: "must not precede linked restore", edit: func(v *RecoveryMetadataGraph) {
			v.Backups[0].Health = BackupHealth{State: BackupHealthUnverified, Evidence: []EvidenceReference{}}
			measured := v.Restores[0].CompletedAt.Add(-time.Second)
			v.ObjectiveEvidence[0].MeasuredAt = measured
			v.ObjectiveEvidence[0].CreatedAt = measured
			v.ObjectiveEvidence[0].UpdatedAt = measured
		}},
		{name: "passed objective from failed restore", want: "PASSED requires a successful", edit: func(v *RecoveryMetadataGraph) {
			v.Backups[0].Health = BackupHealth{State: BackupHealthUnverified, Evidence: []EvidenceReference{}}
			v.Restores[0].State = RestoreFailed
			v.Restores[0].Verification.Outcome = VerificationFailed
			v.Restores[0].Failure = testFailure()
		}},
		{name: "rto underclaim", want: "understates the linked restore", edit: func(v *RecoveryMetadataGraph) {
			v.Backups[0].Health = BackupHealth{State: BackupHealthUnverified, Evidence: []EvidenceReference{}}
			v.ObjectiveEvidence[0].ObservedRTOSeconds = uint64Pointer(1)
		}},
		{name: "rto underclaim beyond duration range", want: "understates the linked restore", edit: func(v *RecoveryMetadataGraph) {
			completed := time.Date(2500, time.January, 1, 0, 0, 0, 0, time.UTC)
			restore := &v.Restores[0]
			restore.CompletedAt = &completed
			restore.UpdatedAt = completed
			restore.Verification.VerifiedAt = &completed
			for index := range restore.Verification.Evidence {
				restore.Verification.Evidence[index].RecordedAt = completed
			}
			v.Backups[0].UpdatedAt = completed
			v.Backups[0].Health.LastVerifiedAt = &completed
			v.Backups[0].Health.Evidence = append([]EvidenceReference(nil), restore.Verification.Evidence...)
			objective := &v.ObjectiveEvidence[0]
			objective.CreatedAt = completed
			objective.UpdatedAt = completed
			objective.MeasuredAt = completed
			objective.TargetRTOSeconds = ^uint64(0)
			objective.ObservedRTOSeconds = uint64Pointer(10_000_000_000)
			objective.Evidence = append([]EvidenceReference(nil), restore.Verification.Evidence...)
		}},
		{name: "record created before measurement", want: "cannot be created before", edit: func(v *RecoveryMetadataGraph) {
			v.Backups[0].Health = BackupHealth{State: BackupHealthUnverified, Evidence: []EvidenceReference{}}
			v.ObjectiveEvidence[0].CreatedAt = v.ObjectiveEvidence[0].MeasuredAt.Add(-time.Second)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph := validRecoveryMetadataGraph()
			test.edit(&graph)
			assertIntegrityRejected(t, graph, test.want)
		})
	}
}

func assertIntegrityRejected(t *testing.T, graph RecoveryMetadataGraph, want string) {
	t.Helper()
	err := ValidateRecoveryMetadataGraph(graph)
	if err == nil {
		t.Fatal("corrupt recovery metadata graph accepted")
	}
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error type = %T, want *ValidationError: %v", err, err)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want substring %q", err, want)
	}
}
