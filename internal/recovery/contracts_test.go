package recovery

import (
	"errors"
	"strings"
	"testing"
	"time"

	"control-center/internal/corecontracts"
)

var testNow = time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

func timePointer(value time.Time) *time.Time { return &value }
func uint64Pointer(value uint64) *uint64     { return &value }

func testObjectMetadata(id string, createdAt, updatedAt time.Time) corecontracts.ObjectMetadata {
	return corecontracts.ObjectMetadata{
		ObjectID: id, ScopeID: "site-a", OwnerScope: "site-a", Generation: 1,
		ResourceVersion: "rv:" + id + ":1", CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
}

func testObject(kind ObjectKind, statefulness Statefulness) ObjectReference {
	return ObjectReference{Kind: kind, ObjectID: "database-primary", ScopeID: "site-a", Statefulness: statefulness}
}

func testEvidence(id string, kind EvidenceKind, recordedAt time.Time) EvidenceReference {
	return EvidenceReference{
		ID: id, Kind: kind, Reference: "urn:cc:evidence:" + id,
		Digest: "sha256:" + strings.Repeat("a", 64), RecordedAt: recordedAt,
	}
}

func testFailure() *FailureMetadata {
	return &FailureMetadata{Code: "PROVIDER_FAILURE", Message: "provider operation failed", Retryable: true}
}

func validRecoveryPoint() RecoveryPoint {
	expires := testNow.Add(24 * time.Hour)
	return RecoveryPoint{
		ObjectMetadata: testObjectMetadata("rp-20260908-001", testNow, testNow.Add(time.Minute)),
		SchemaVersion:  RecoveryPointSchemaVersion,
		State:          RecoveryPointReady, Trigger: TriggerPreDestructive, Consistency: ConsistencyApplication,
		Objects: []ObjectReference{testObject(ObjectDatabase, Stateful)}, BackupIDs: []string{"backup-001"},
		ChangeID: "change-001", ExpiresAt: &expires,
	}
}

func TestValidateRecoveryPointReady(t *testing.T) {
	if err := ValidateRecoveryPoint(validRecoveryPoint()); err != nil {
		t.Fatalf("ValidateRecoveryPoint() error = %v", err)
	}
	creating := validRecoveryPoint()
	creating.State = RecoveryPointCreating
	creating.BackupIDs = []string{}
	creating.UpdatedAt = creating.CreatedAt
	if err := ValidateRecoveryPoint(creating); err != nil {
		t.Fatalf("creating recovery point rejected: %v", err)
	}
}

func TestValidateRecoveryPointRejectsInvalidMetadata(t *testing.T) {
	tests := []struct {
		name string
		edit func(*RecoveryPoint)
	}{
		{name: "schema", edit: func(v *RecoveryPoint) { v.SchemaVersion = "recovery.point/v0" }},
		{name: "id", edit: func(v *RecoveryPoint) { v.ObjectID = " RP " }},
		{name: "scope", edit: func(v *RecoveryPoint) { v.ScopeID = "" }},
		{name: "owner scope", edit: func(v *RecoveryPoint) { v.OwnerScope = "" }},
		{name: "generation", edit: func(v *RecoveryPoint) { v.Generation = 0 }},
		{name: "resource version", edit: func(v *RecoveryPoint) { v.ResourceVersion = "" }},
		{name: "resource version whitespace", edit: func(v *RecoveryPoint) { v.ResourceVersion = "rv:bad version" }},
		{name: "state", edit: func(v *RecoveryPoint) { v.State = "SUCCESS" }},
		{name: "trigger", edit: func(v *RecoveryPoint) { v.Trigger = "AUTOMATIC" }},
		{name: "consistency", edit: func(v *RecoveryPoint) { v.Consistency = "UNKNOWN" }},
		{name: "objects missing", edit: func(v *RecoveryPoint) { v.Objects = nil }},
		{name: "object kind", edit: func(v *RecoveryPoint) { v.Objects[0].Kind = "FILE" }},
		{name: "object statefulness", edit: func(v *RecoveryPoint) { v.Objects[0].Statefulness = "MAYBE" }},
		{name: "duplicate object", edit: func(v *RecoveryPoint) { v.Objects = append(v.Objects, v.Objects[0]) }},
		{name: "cross-scope object", edit: func(v *RecoveryPoint) { v.Objects[0].ScopeID = "site-b" }},
		{name: "null backup array", edit: func(v *RecoveryPoint) { v.State = RecoveryPointCreating; v.BackupIDs = nil }},
		{name: "duplicate backup", edit: func(v *RecoveryPoint) { v.BackupIDs = append(v.BackupIDs, v.BackupIDs[0]) }},
		{name: "pre-change missing change", edit: func(v *RecoveryPoint) { v.ChangeID = "" }},
		{name: "created timestamp", edit: func(v *RecoveryPoint) { v.CreatedAt = time.Time{} }},
		{name: "non utc", edit: func(v *RecoveryPoint) { v.UpdatedAt = v.UpdatedAt.In(time.FixedZone("east", 3600)) }},
		{name: "time reversal", edit: func(v *RecoveryPoint) { v.UpdatedAt = v.CreatedAt.Add(-time.Second) }},
		{name: "bad expiration", edit: func(v *RecoveryPoint) { value := v.CreatedAt; v.ExpiresAt = &value }},
		{name: "ready missing backup", edit: func(v *RecoveryPoint) { v.BackupIDs = nil }},
		{name: "ready with failure", edit: func(v *RecoveryPoint) { v.Failure = testFailure() }},
		{name: "partial missing failure", edit: func(v *RecoveryPoint) { v.State = RecoveryPointPartial }},
		{name: "failed missing failure", edit: func(v *RecoveryPoint) { v.State = RecoveryPointFailed }},
		{name: "failed with successful backup", edit: func(v *RecoveryPoint) { v.State = RecoveryPointFailed; v.Failure = testFailure() }},
		{name: "expired in future", edit: func(v *RecoveryPoint) { v.State = RecoveryPointExpired }},
		{name: "elapsed not expired", edit: func(v *RecoveryPoint) { value := v.CreatedAt.Add(30 * time.Second); v.ExpiresAt = &value }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validRecoveryPoint()
			test.edit(&value)
			err := ValidateRecoveryPoint(value)
			if err == nil {
				t.Fatal("invalid recovery point accepted")
			}
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error type = %T, want *ValidationError", err)
			}
		})
	}
}

func validObjectiveEvidence() RecoveryObjectiveEvidence {
	measuredAt := testNow.Add(3 * time.Hour)
	return RecoveryObjectiveEvidence{
		ObjectMetadata: testObjectMetadata("objective-evidence-001", measuredAt, measuredAt),
		SchemaVersion:  ObjectiveEvidenceSchemaVersion,
		PolicyID:       "gold-policy", Target: testObject(ObjectDatabase, Stateful),
		TargetRPOSeconds: 3600, TargetRTOSeconds: 900,
		ObservedRPOSeconds: uint64Pointer(300), ObservedRTOSeconds: uint64Pointer(600),
		RecoveryPointID: "rp-20260908-001", RestoreID: "restore-drill-001",
		MeasuredAt: measuredAt, Result: ObjectivePassed,
		Evidence: []EvidenceReference{
			testEvidence("restore-drill-report", EvidenceRestoreDrill, testNow.Add(2*time.Hour)),
			testEvidence("functional-checks", EvidenceFunctionalTest, testNow.Add(2*time.Hour+time.Minute)),
		},
	}
}

func TestValidateRecoveryObjectiveEvidence(t *testing.T) {
	if err := ValidateRecoveryObjectiveEvidence(validObjectiveEvidence()); err != nil {
		t.Fatalf("ValidateRecoveryObjectiveEvidence() error = %v", err)
	}
	failed := validObjectiveEvidence()
	failed.Result = ObjectiveFailed
	failed.ObservedRPOSeconds = nil
	failed.ObservedRTOSeconds = nil
	failed.FailureReason = "functional verification failed"
	failed.Evidence = failed.Evidence[:1]
	if err := ValidateRecoveryObjectiveEvidence(failed); err != nil {
		t.Fatalf("failed objective evidence rejected: %v", err)
	}
}

func TestValidateRecoveryObjectiveEvidenceRejectsUnprovenClaims(t *testing.T) {
	tests := []struct {
		name string
		edit func(*RecoveryObjectiveEvidence)
	}{
		{name: "schema", edit: func(v *RecoveryObjectiveEvidence) { v.SchemaVersion = "wrong" }},
		{name: "policy", edit: func(v *RecoveryObjectiveEvidence) { v.PolicyID = "" }},
		{name: "cross-scope target", edit: func(v *RecoveryObjectiveEvidence) { v.Target.ScopeID = "site-b" }},
		{name: "zero target", edit: func(v *RecoveryObjectiveEvidence) { v.TargetRPOSeconds = 0 }},
		{name: "result", edit: func(v *RecoveryObjectiveEvidence) { v.Result = "UNKNOWN" }},
		{name: "measurement after metadata update", edit: func(v *RecoveryObjectiveEvidence) { v.MeasuredAt = v.UpdatedAt.Add(time.Second) }},
		{name: "no evidence", edit: func(v *RecoveryObjectiveEvidence) { v.Evidence = nil }},
		{name: "no restore drill", edit: func(v *RecoveryObjectiveEvidence) { v.Evidence = v.Evidence[1:] }},
		{name: "evidence after measurement", edit: func(v *RecoveryObjectiveEvidence) { v.Evidence[0].RecordedAt = v.MeasuredAt.Add(time.Second) }},
		{name: "passed no observations", edit: func(v *RecoveryObjectiveEvidence) { v.ObservedRPOSeconds = nil }},
		{name: "passed exceeds rto", edit: func(v *RecoveryObjectiveEvidence) { v.ObservedRTOSeconds = uint64Pointer(v.TargetRTOSeconds + 1) }},
		{name: "passed has failure", edit: func(v *RecoveryObjectiveEvidence) { v.FailureReason = "failed" }},
		{name: "passed no functional evidence", edit: func(v *RecoveryObjectiveEvidence) { v.Evidence = v.Evidence[:1] }},
		{name: "failed without reason", edit: func(v *RecoveryObjectiveEvidence) {
			v.Result = ObjectiveFailed
			v.ObservedRPOSeconds = nil
			v.ObservedRTOSeconds = nil
			v.FailureReason = ""
		}},
		{name: "duplicate evidence", edit: func(v *RecoveryObjectiveEvidence) { v.Evidence = append(v.Evidence, v.Evidence[0]) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validObjectiveEvidence()
			test.edit(&value)
			if err := ValidateRecoveryObjectiveEvidence(value); err == nil {
				t.Fatal("invalid objective evidence accepted")
			}
		})
	}
}
