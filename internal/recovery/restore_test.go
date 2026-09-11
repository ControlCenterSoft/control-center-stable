package recovery

import (
	"testing"
	"time"
)

func restoreProvider(capabilities ...ProviderCapability) ProviderMetadata {
	return ProviderMetadata{
		ProviderID: "pgbackrest", AdapterID: "postgres.pgbackrest", Version: "2.54.2",
		RepositoryID: "backup-repository-a", OperationID: "restore-operation-001", Capabilities: capabilities,
	}
}

func noFencing() FencingMetadata {
	return FencingMetadata{State: FencingNotRequired, TargetIDs: []string{}, Evidence: []EvidenceReference{}}
}

func confirmedFencing() FencingMetadata {
	confirmedAt := testNow.Add(30 * time.Second)
	return FencingMetadata{
		State: FencingConfirmed, TargetIDs: []string{"node-primary"}, Epoch: 7,
		Provider: &ProviderMetadata{
			ProviderID: "stonith", AdapterID: "cluster.stonith", Version: "1.0.0",
			OperationID: "fence-operation-001", Capabilities: []ProviderCapability{CapabilityFencing},
		},
		ConfirmedAt: &confirmedAt,
		Evidence:    []EvidenceReference{testEvidence("fence-proof", EvidenceFencingConfirmation, confirmedAt)},
	}
}

func validRestoreMetadata() RestoreMetadata {
	started := testNow.Add(time.Minute)
	verified := testNow.Add(9 * time.Minute)
	completed := testNow.Add(10 * time.Minute)
	return RestoreMetadata{
		ObjectMetadata:  testObjectMetadata("restore-drill-001", testNow, completed),
		SchemaVersion:   RestoreMetadataSchemaVersion,
		RecoveryPointID: "rp-20260908-001", BackupIDs: []string{"backup-001"},
		Mode: RestoreIsolatedDrill, State: RestoreSucceeded, Target: testObject(ObjectDatabase, Stateful),
		RequestedObjects: []ObjectReference{}, Provider: restoreProvider(CapabilityRestore, CapabilityRestoreDrill),
		Fencing: noFencing(), RequestedAt: testNow, StartedAt: &started, CompletedAt: &completed,
		Verification: RestoreVerification{
			Outcome: VerificationPassed, VerifiedAt: &verified,
			Evidence: []EvidenceReference{
				testEvidence("restore-proof", EvidenceRestoreDrill, verified),
				testEvidence("functional-proof", EvidenceFunctionalTest, verified),
			},
		},
	}
}

func TestValidateRestoreMetadataIsolatedDrill(t *testing.T) {
	if err := ValidateRestoreMetadata(validRestoreMetadata()); err != nil {
		t.Fatalf("ValidateRestoreMetadata() error = %v", err)
	}
}

func TestValidateRestoreMetadataStatefulInPlaceFencing(t *testing.T) {
	restore := validRestoreMetadata()
	restore.ObjectID = "restore-in-place-001"
	restore.ResourceVersion = "rv:restore-in-place-001:1"
	restore.Mode = RestoreInPlace
	restore.Provider.Capabilities = []ProviderCapability{CapabilityRestore}
	restore.Fencing = confirmedFencing()
	restore.Verification.Evidence = []EvidenceReference{testEvidence("integrity-proof", EvidenceChecksum, *restore.Verification.VerifiedAt)}
	if err := ValidateRestoreMetadata(restore); err != nil {
		t.Fatalf("stateful in-place restore rejected: %v", err)
	}

	planned := restore
	planned.State = RestorePlanned
	planned.Provider.OperationID = ""
	planned.StartedAt, planned.CompletedAt = nil, nil
	planned.Verification = RestoreVerification{Outcome: VerificationPending, Evidence: []EvidenceReference{}}
	planned.Fencing = FencingMetadata{
		State: FencingRequired, TargetIDs: []string{"node-primary"}, Epoch: 8,
		Provider: &ProviderMetadata{ProviderID: "stonith", AdapterID: "cluster.stonith", Version: "1.0.0", Capabilities: []ProviderCapability{CapabilityFencing}},
		Evidence: []EvidenceReference{},
	}
	if err := ValidateRestoreMetadata(planned); err != nil {
		t.Fatalf("planned stateful restore rejected: %v", err)
	}

	cancelledBeforeStart := planned
	cancelledBeforeStart.State = RestoreCancelled
	cancelledBeforeStart.CompletedAt = timePointer(testNow.Add(time.Minute))
	if err := ValidateRestoreMetadata(cancelledBeforeStart); err != nil {
		t.Fatalf("stateful restore cancelled before start rejected: %v", err)
	}

	cancelledAfterStart := restore
	cancelledAfterStart.State = RestoreCancelled
	cancelledAfterStart.Verification = RestoreVerification{Outcome: VerificationPending, Evidence: []EvidenceReference{}}
	if err := ValidateRestoreMetadata(cancelledAfterStart); err != nil {
		t.Fatalf("stateful restore cancelled after start rejected: %v", err)
	}
}

func TestValidateRestoreMetadataObjectAndPITRModes(t *testing.T) {
	objectRestore := validRestoreMetadata()
	objectRestore.Mode = RestoreObjectLevel
	objectRestore.Provider.Capabilities = []ProviderCapability{CapabilityRestore, CapabilityObjectRestore}
	objectRestore.RequestedObjects = []ObjectReference{{Kind: ObjectUser, ObjectID: "user-alice", ScopeID: "site-a", Statefulness: Stateless}}
	objectRestore.Verification.Evidence = []EvidenceReference{testEvidence("object-proof", EvidenceChecksum, *objectRestore.Verification.VerifiedAt)}
	if err := ValidateRestoreMetadata(objectRestore); err != nil {
		t.Fatalf("object-level restore rejected: %v", err)
	}

	pitr := validRestoreMetadata()
	pitr.ObjectID = "restore-pitr-001"
	pitr.ResourceVersion = "rv:restore-pitr-001:1"
	pitr.Mode = RestorePITR
	pitr.Provider.Capabilities = []ProviderCapability{CapabilityRestore, CapabilityPITR}
	pitr.Fencing = confirmedFencing()
	pitr.PITRTargetAt = timePointer(testNow.Add(-time.Hour))
	pitr.Verification.Evidence = []EvidenceReference{testEvidence("pitr-proof", EvidenceChecksum, *pitr.Verification.VerifiedAt)}
	if err := ValidateRestoreMetadata(pitr); err != nil {
		t.Fatalf("PITR restore rejected: %v", err)
	}
}

func TestValidateRestoreMetadataRejectsInvalidContracts(t *testing.T) {
	tests := []struct {
		name string
		edit func(*RestoreMetadata)
	}{
		{name: "schema", edit: func(v *RestoreMetadata) { v.SchemaVersion = "restore/v0" }},
		{name: "recovery point", edit: func(v *RestoreMetadata) { v.RecoveryPointID = "" }},
		{name: "cross-scope target", edit: func(v *RestoreMetadata) { v.Target.ScopeID = "site-b" }},
		{name: "backup missing", edit: func(v *RestoreMetadata) { v.BackupIDs = nil }},
		{name: "backup duplicate", edit: func(v *RestoreMetadata) { v.BackupIDs = append(v.BackupIDs, v.BackupIDs[0]) }},
		{name: "mode", edit: func(v *RestoreMetadata) { v.Mode = "MAGIC" }},
		{name: "state", edit: func(v *RestoreMetadata) { v.State = "DONE" }},
		{name: "provider restore capability", edit: func(v *RestoreMetadata) { v.Provider.Capabilities = []ProviderCapability{CapabilityRestoreDrill} }},
		{name: "drill capability", edit: func(v *RestoreMetadata) { v.Provider.Capabilities = []ProviderCapability{CapabilityRestore} }},
		{name: "object list missing", edit: func(v *RestoreMetadata) {
			v.Mode = RestoreObjectLevel
			v.Provider.Capabilities = []ProviderCapability{CapabilityRestore, CapabilityObjectRestore}
			v.RequestedObjects = nil
			v.Verification.Evidence = []EvidenceReference{testEvidence("proof", EvidenceChecksum, *v.Verification.VerifiedAt)}
		}},
		{name: "object list unexpected", edit: func(v *RestoreMetadata) { v.RequestedObjects = []ObjectReference{testObject(ObjectDatabase, Stateful)} }},
		{name: "pitr target missing", edit: func(v *RestoreMetadata) {
			v.Mode = RestorePITR
			v.Provider.Capabilities = []ProviderCapability{CapabilityRestore, CapabilityPITR}
			v.Fencing = confirmedFencing()
			v.Verification.Evidence = []EvidenceReference{testEvidence("proof", EvidenceChecksum, *v.Verification.VerifiedAt)}
		}},
		{name: "pitr target future", edit: func(v *RestoreMetadata) {
			v.Mode = RestorePITR
			v.Provider.Capabilities = []ProviderCapability{CapabilityRestore, CapabilityPITR}
			v.Fencing = confirmedFencing()
			v.PITRTargetAt = timePointer(v.RequestedAt.Add(time.Hour))
			v.Verification.Evidence = []EvidenceReference{testEvidence("proof", EvidenceChecksum, *v.Verification.VerifiedAt)}
		}},
		{name: "planned has results", edit: func(v *RestoreMetadata) { v.State = RestorePlanned }},
		{name: "completion after metadata update", edit: func(v *RestoreMetadata) { v.UpdatedAt = v.CompletedAt.Add(-time.Second) }},
		{name: "running missing operation", edit: func(v *RestoreMetadata) {
			v.State = RestoreRunning
			v.Provider.OperationID = ""
			v.CompletedAt = nil
			v.Verification = RestoreVerification{Outcome: VerificationPending, Evidence: []EvidenceReference{}}
		}},
		{name: "success pending verification", edit: func(v *RestoreMetadata) {
			v.Verification = RestoreVerification{Outcome: VerificationPending, Evidence: []EvidenceReference{}}
		}},
		{name: "terminal verification without start", edit: func(v *RestoreMetadata) {
			v.State = RestoreFailed
			v.StartedAt = nil
			v.Verification.Outcome = VerificationFailed
			v.Failure = testFailure()
		}},
		{name: "cancelled operation without start", edit: func(v *RestoreMetadata) {
			v.State = RestoreCancelled
			v.StartedAt = nil
			v.Verification = RestoreVerification{Outcome: VerificationPending, Evidence: []EvidenceReference{}}
		}},
		{name: "null verification evidence", edit: func(v *RestoreMetadata) { v.Verification.Evidence = nil }},
		{name: "drill missing functional proof", edit: func(v *RestoreMetadata) { v.Verification.Evidence = v.Verification.Evidence[:1] }},
		{name: "verification evidence after timestamp", edit: func(v *RestoreMetadata) {
			v.Verification.Evidence[0].RecordedAt = v.Verification.VerifiedAt.Add(time.Second)
		}},
		{name: "verification before start", edit: func(v *RestoreMetadata) { v.Verification.VerifiedAt = timePointer(v.StartedAt.Add(-time.Second)) }},
		{name: "verification after completion", edit: func(v *RestoreMetadata) { v.Verification.VerifiedAt = timePointer(v.CompletedAt.Add(time.Second)) }},
		{name: "failed no failure", edit: func(v *RestoreMetadata) {
			v.State = RestoreFailed
			v.Verification = RestoreVerification{Outcome: VerificationPending, Evidence: []EvidenceReference{}}
		}},
		{name: "alternate unexpected fence", edit: func(v *RestoreMetadata) {
			v.Mode = RestoreAlternate
			v.Provider.Capabilities = []ProviderCapability{CapabilityRestore}
			v.Fencing = confirmedFencing()
			v.Verification.Evidence = []EvidenceReference{testEvidence("proof", EvidenceChecksum, *v.Verification.VerifiedAt)}
		}},
		{name: "in-place stateful unfenced", edit: func(v *RestoreMetadata) {
			v.Mode = RestoreInPlace
			v.Provider.Capabilities = []ProviderCapability{CapabilityRestore}
			v.Verification.Evidence = []EvidenceReference{testEvidence("proof", EvidenceChecksum, *v.Verification.VerifiedAt)}
		}},
		{name: "fence confirmation after start", edit: func(v *RestoreMetadata) {
			v.Mode = RestoreInPlace
			v.Provider.Capabilities = []ProviderCapability{CapabilityRestore}
			v.Fencing = confirmedFencing()
			v.Fencing.ConfirmedAt = timePointer(v.StartedAt.Add(time.Second))
			v.Verification.Evidence = []EvidenceReference{testEvidence("proof", EvidenceChecksum, *v.Verification.VerifiedAt)}
		}},
		{name: "fence confirmation before request", edit: func(v *RestoreMetadata) {
			v.Mode = RestoreInPlace
			v.Provider.Capabilities = []ProviderCapability{CapabilityRestore}
			v.Fencing = confirmedFencing()
			v.Fencing.ConfirmedAt = timePointer(v.RequestedAt.Add(-time.Second))
			v.Fencing.Evidence[0].RecordedAt = *v.Fencing.ConfirmedAt
			v.Verification.Evidence = []EvidenceReference{testEvidence("proof", EvidenceChecksum, *v.Verification.VerifiedAt)}
		}},
		{name: "fence evidence after confirmation", edit: func(v *RestoreMetadata) {
			v.Mode = RestoreInPlace
			v.Provider.Capabilities = []ProviderCapability{CapabilityRestore}
			v.Fencing = confirmedFencing()
			v.Fencing.Evidence[0].RecordedAt = v.Fencing.ConfirmedAt.Add(time.Second)
			v.Verification.Evidence = []EvidenceReference{testEvidence("proof", EvidenceChecksum, *v.Verification.VerifiedAt)}
		}},
		{name: "fence missing capability", edit: func(v *RestoreMetadata) {
			v.Mode = RestoreInPlace
			v.Provider.Capabilities = []ProviderCapability{CapabilityRestore}
			v.Fencing = confirmedFencing()
			v.Fencing.Provider.Capabilities = []ProviderCapability{CapabilityRestore}
			v.Verification.Evidence = []EvidenceReference{testEvidence("proof", EvidenceChecksum, *v.Verification.VerifiedAt)}
		}},
		{name: "fence missing evidence", edit: func(v *RestoreMetadata) {
			v.Mode = RestoreInPlace
			v.Provider.Capabilities = []ProviderCapability{CapabilityRestore}
			v.Fencing = confirmedFencing()
			v.Fencing.Evidence = nil
			v.Verification.Evidence = []EvidenceReference{testEvidence("proof", EvidenceChecksum, *v.Verification.VerifiedAt)}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validRestoreMetadata()
			test.edit(&value)
			if err := ValidateRestoreMetadata(value); err == nil {
				t.Fatal("invalid restore metadata accepted")
			}
		})
	}
}
