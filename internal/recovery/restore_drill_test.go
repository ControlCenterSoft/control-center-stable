package recovery

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"control-center/internal/corecontracts"
)

func plannedRestoreDrill() RestoreMetadata {
	restore := validRestoreMetadata()
	restore.State = RestorePlanned
	restore.UpdatedAt = restore.RequestedAt
	restore.Provider.OperationID = ""
	restore.StartedAt = nil
	restore.CompletedAt = nil
	restore.Verification = RestoreVerification{Outcome: VerificationPending, Evidence: []EvidenceReference{}}
	return restore
}

func drillPrecondition(restore RestoreMetadata) corecontracts.ObjectPrecondition {
	generation := restore.Generation
	return corecontracts.ObjectPrecondition{
		ObjectID: restore.ObjectID, ResourceVersion: restore.ResourceVersion, Generation: &generation,
	}
}

func drillTransition(restore RestoreMetadata, to RestoreState, at time.Time) RestoreDrillTransitionRequest {
	return RestoreDrillTransitionRequest{
		To: to, Precondition: drillPrecondition(restore), OccurredAt: at,
	}
}

func TestBuildRestoreDrillPlanIsDeterministicAndNonExecuting(t *testing.T) {
	registry := registeredRestoreDrillRegistry(t)
	restore := plannedRestoreDrill()
	restore.BackupIDs = []string{"backup-002", "backup-001"}
	restore.Provider.Capabilities = []ProviderCapability{CapabilityRestoreDrill, CapabilityRestore}
	before := cloneRestoreMetadata(restore)

	first, err := BuildRestoreDrillPlan(registry, restore, restore.UpdatedAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("BuildRestoreDrillPlan() error = %v", err)
	}
	if !first.PlanOnly || first.ExecutesProvider || first.UsesCredentials || first.ProductionMutation || !first.RequiresAuditedChangeJob {
		t.Fatalf("unsafe plan flags: %#v", first)
	}
	if first.SchemaVersion != RestoreDrillPlanSchemaVersion || !strings.HasPrefix(first.PlanID, "rdp-") {
		t.Fatalf("plan identity = %#v", first)
	}
	if !reflect.DeepEqual(first.BackupIDs, []string{"backup-001", "backup-002"}) {
		t.Fatalf("backup IDs not canonical: %#v", first.BackupIDs)
	}
	if len(first.Steps) != 6 {
		t.Fatalf("steps = %#v", first.Steps)
	}
	for index, step := range first.Steps {
		if step.Order != uint32(index+1) {
			t.Fatalf("step %d order = %d", index, step.Order)
		}
	}
	if !reflect.DeepEqual(restore, before) {
		t.Fatalf("planner mutated restore: before=%#v after=%#v", before, restore)
	}

	reordered := cloneRestoreMetadata(restore)
	reordered.BackupIDs[0], reordered.BackupIDs[1] = reordered.BackupIDs[1], reordered.BackupIDs[0]
	reordered.Provider.Capabilities[0], reordered.Provider.Capabilities[1] = reordered.Provider.Capabilities[1], reordered.Provider.Capabilities[0]
	second, err := BuildRestoreDrillPlan(registry, reordered, restore.UpdatedAt.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if second.PlanID != first.PlanID {
		t.Fatalf("equivalent inputs produce different plan IDs: %s != %s", first.PlanID, second.PlanID)
	}
	first.BackupIDs[0] = "mutated"
	third, err := BuildRestoreDrillPlan(registry, restore, restore.UpdatedAt.Add(3*time.Minute))
	if err != nil || third.BackupIDs[0] != "backup-001" {
		t.Fatalf("returned plan aliases inputs: plan=%#v err=%v", third, err)
	}
}

func TestBuildRestoreDrillPlanFailsClosed(t *testing.T) {
	valid := plannedRestoreDrill()
	tests := []struct {
		name     string
		registry *AdapterRegistry
		mutate   func(*RestoreMetadata)
		at       time.Time
		want     error
	}{
		{name: "nil registry", registry: nil, mutate: func(*RestoreMetadata) {}, at: valid.UpdatedAt.Add(time.Minute), want: ErrAdapterRegistryUnavailable},
		{name: "unregistered", registry: NewAdapterRegistry(), mutate: func(*RestoreMetadata) {}, at: valid.UpdatedAt.Add(time.Minute), want: ErrAdapterNotRegistered},
		{name: "capability mismatch", registry: registeredRestoreDrillRegistry(t), mutate: func(value *RestoreMetadata) {
			value.Provider.Capabilities = append(value.Provider.Capabilities, CapabilityPITR)
		}, at: valid.UpdatedAt.Add(time.Minute), want: ErrAdapterCapabilityMismatch},
		{name: "not planned", registry: registeredRestoreDrillRegistry(t), mutate: func(value *RestoreMetadata) { *value = validRestoreMetadata() }, at: validRestoreMetadata().UpdatedAt.Add(time.Minute), want: ErrInvalidRestoreDrillPlan},
		{name: "stale evaluation", registry: registeredRestoreDrillRegistry(t), mutate: func(*RestoreMetadata) {}, at: valid.UpdatedAt, want: ErrInvalidRestoreDrillPlan},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			restore := cloneRestoreMetadata(valid)
			test.mutate(&restore)
			if _, err := BuildRestoreDrillPlan(test.registry, restore, test.at); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestApplyRestoreDrillTransitionLifecycleAndEvidence(t *testing.T) {
	registry := registeredRestoreDrillRegistry(t)
	planned := plannedRestoreDrill()

	start := drillTransition(planned, RestoreRunning, testNow.Add(time.Minute))
	start.ProviderOperationID = "provider-restore-drill-001"
	running, err := ApplyRestoreDrillTransition(registry, planned, start, "rv:restore-drill-001:2")
	if err != nil {
		t.Fatalf("start transition error = %v", err)
	}
	if running.State != RestoreRunning || running.StartedAt == nil || running.Provider.OperationID != start.ProviderOperationID || running.Generation != planned.Generation {
		t.Fatalf("running metadata = %#v", running)
	}

	verify := drillTransition(running, RestoreVerifying, testNow.Add(2*time.Minute))
	verifying, err := ApplyRestoreDrillTransition(registry, running, verify, "rv:restore-drill-001:3")
	if err != nil {
		t.Fatalf("verify transition error = %v", err)
	}

	complete := drillTransition(verifying, RestoreSucceeded, testNow.Add(4*time.Minute))
	complete.VerificationEvidence = []EvidenceReference{
		testEvidence("functional-proof", EvidenceFunctionalTest, testNow.Add(3*time.Minute)),
		testEvidence("restore-proof", EvidenceRestoreDrill, testNow.Add(2*time.Minute+30*time.Second)),
	}
	succeeded, err := ApplyRestoreDrillTransition(registry, verifying, complete, "rv:restore-drill-001:4")
	if err != nil {
		t.Fatalf("success transition error = %v", err)
	}
	if succeeded.State != RestoreSucceeded || succeeded.CompletedAt == nil || succeeded.Verification.VerifiedAt == nil || succeeded.Verification.Outcome != VerificationPassed {
		t.Fatalf("succeeded metadata = %#v", succeeded)
	}
	if succeeded.Verification.Evidence[0].Kind != EvidenceFunctionalTest || succeeded.Verification.Evidence[1].Kind != EvidenceRestoreDrill {
		t.Fatalf("evidence is not canonical: %#v", succeeded.Verification.Evidence)
	}
	if err := ValidateRestoreMetadata(succeeded); err != nil {
		t.Fatalf("successor failed contract validation: %v", err)
	}

	again, err := ApplyRestoreDrillTransition(registry, verifying, complete, "rv:restore-drill-001:4")
	if err != nil || !reflect.DeepEqual(again, succeeded) {
		t.Fatalf("transition is not deterministic: again=%#v err=%v", again, err)
	}
	complete.VerificationEvidence[0].ID = "mutated"
	if succeeded.Verification.Evidence[0].ID == "mutated" {
		t.Fatal("successor aliases request evidence")
	}
}

func TestApplyRestoreDrillTransitionFailureAndCancellation(t *testing.T) {
	registry := registeredRestoreDrillRegistry(t)
	planned := plannedRestoreDrill()

	cancel := drillTransition(planned, RestoreCancelled, testNow.Add(time.Minute))
	cancelled, err := ApplyRestoreDrillTransition(registry, planned, cancel, "rv:cancelled")
	if err != nil || cancelled.State != RestoreCancelled || cancelled.StartedAt != nil {
		t.Fatalf("planned cancellation = %#v err=%v", cancelled, err)
	}

	start := drillTransition(planned, RestoreRunning, testNow.Add(time.Minute))
	start.ProviderOperationID = "provider-restore-drill-001"
	running, err := ApplyRestoreDrillTransition(registry, planned, start, "rv:running")
	if err != nil {
		t.Fatal(err)
	}
	fail := drillTransition(running, RestoreFailed, testNow.Add(2*time.Minute))
	fail.Failure = testFailure()
	failed, err := ApplyRestoreDrillTransition(registry, running, fail, "rv:failed")
	if err != nil || failed.State != RestoreFailed || failed.Verification.Outcome != VerificationPending {
		t.Fatalf("execution failure = %#v err=%v", failed, err)
	}

	verify := drillTransition(running, RestoreVerifying, testNow.Add(2*time.Minute))
	verifying, err := ApplyRestoreDrillTransition(registry, running, verify, "rv:verifying")
	if err != nil {
		t.Fatal(err)
	}
	verificationFail := drillTransition(verifying, RestoreFailed, testNow.Add(4*time.Minute))
	verificationFail.Failure = &FailureMetadata{Code: "FUNCTIONAL_TEST_FAILED", Message: "restored service did not pass its functional checks", Retryable: true}
	verificationFail.VerificationEvidence = []EvidenceReference{testEvidence("restore-proof", EvidenceRestoreDrill, testNow.Add(3*time.Minute))}
	verificationFailed, err := ApplyRestoreDrillTransition(registry, verifying, verificationFail, "rv:verification-failed")
	if err != nil || verificationFailed.Verification.Outcome != VerificationFailed {
		t.Fatalf("verification failure = %#v err=%v", verificationFailed, err)
	}
}

func TestApplyRestoreDrillTransitionRejectsUnsafeInputs(t *testing.T) {
	registry := registeredRestoreDrillRegistry(t)
	planned := plannedRestoreDrill()
	validStart := drillTransition(planned, RestoreRunning, testNow.Add(time.Minute))
	validStart.ProviderOperationID = "provider-restore-drill-001"

	t.Run("stale CAS precondition", func(t *testing.T) {
		request := validStart
		request.Precondition.ResourceVersion = "rv:stale"
		if _, err := ApplyRestoreDrillTransition(registry, planned, request, "rv:running"); !errors.Is(err, corecontracts.ErrPreconditionFailed) {
			t.Fatalf("error = %v, want ErrPreconditionFailed", err)
		}
	})
	t.Run("invalid transition", func(t *testing.T) {
		request := drillTransition(planned, RestoreSucceeded, testNow.Add(time.Minute))
		request.VerificationEvidence = []EvidenceReference{
			testEvidence("restore-proof", EvidenceRestoreDrill, testNow.Add(30*time.Second)),
			testEvidence("functional-proof", EvidenceFunctionalTest, testNow.Add(30*time.Second)),
		}
		if _, err := ApplyRestoreDrillTransition(registry, planned, request, "rv:success"); !errors.Is(err, ErrInvalidRestoreDrillTransition) {
			t.Fatalf("error = %v, want ErrInvalidRestoreDrillTransition", err)
		}
	})
	t.Run("adapter removed or absent", func(t *testing.T) {
		if _, err := ApplyRestoreDrillTransition(NewAdapterRegistry(), planned, validStart, "rv:running"); !errors.Is(err, ErrAdapterNotRegistered) {
			t.Fatalf("error = %v, want ErrAdapterNotRegistered", err)
		}
	})
	t.Run("resource version reuse", func(t *testing.T) {
		request := validStart
		if _, err := ApplyRestoreDrillTransition(registry, planned, request, planned.ResourceVersion); !errors.Is(err, corecontracts.ErrInvalidTransition) {
			t.Fatalf("error = %v, want ErrInvalidTransition", err)
		}
	})
}

func TestRestoreDrillTransitionRequestCannotSupplyResourceVersion(t *testing.T) {
	registry := registeredRestoreDrillRegistry(t)
	planned := plannedRestoreDrill()
	request := drillTransition(planned, RestoreRunning, testNow.Add(time.Minute))
	request.ProviderOperationID = "provider-restore-drill-001"

	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"new_resource_version"`) || strings.Contains(string(encoded), `"next_resource_version"`) {
		t.Fatalf("transition request exposes successor resource version: %s", encoded)
	}

	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	payload["new_resource_version"] = "rv:client-controlled"
	payload["next_resource_version"] = "rv:client-controlled"
	payload["resource_version"] = "rv:client-controlled"
	tampered, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded RestoreDrillTransitionRequest
	if err := json.Unmarshal(tampered, &decoded); err != nil {
		t.Fatal(err)
	}

	next, err := ApplyRestoreDrillTransition(registry, planned, decoded, "rv:server-allocated")
	if err != nil {
		t.Fatal(err)
	}
	if next.ResourceVersion != "rv:server-allocated" {
		t.Fatalf("resource version = %q, want server allocation", next.ResourceVersion)
	}
}

func TestApplyRestoreDrillTransitionRejectsUnverifiableEvidence(t *testing.T) {
	registry := registeredRestoreDrillRegistry(t)
	planned := plannedRestoreDrill()
	start := drillTransition(planned, RestoreRunning, testNow.Add(time.Minute))
	start.ProviderOperationID = "provider-restore-drill-001"
	running, err := ApplyRestoreDrillTransition(registry, planned, start, "rv:running")
	if err != nil {
		t.Fatal(err)
	}
	verify := drillTransition(running, RestoreVerifying, testNow.Add(2*time.Minute))
	verifying, err := ApplyRestoreDrillTransition(registry, running, verify, "rv:verifying")
	if err != nil {
		t.Fatal(err)
	}

	valid := []EvidenceReference{
		testEvidence("restore-proof", EvidenceRestoreDrill, testNow.Add(3*time.Minute)),
		testEvidence("functional-proof", EvidenceFunctionalTest, testNow.Add(3*time.Minute)),
	}
	tests := []struct {
		name   string
		mutate func(*[]EvidenceReference)
	}{
		{name: "invalid digest", mutate: func(evidence *[]EvidenceReference) { (*evidence)[0].Digest = "sha256:untrusted" }},
		{name: "missing restore proof", mutate: func(evidence *[]EvidenceReference) { *evidence = (*evidence)[1:] }},
		{name: "missing functional proof", mutate: func(evidence *[]EvidenceReference) { *evidence = (*evidence)[:1] }},
		{name: "evidence before start", mutate: func(evidence *[]EvidenceReference) { (*evidence)[0].RecordedAt = testNow }},
		{name: "evidence after result", mutate: func(evidence *[]EvidenceReference) { (*evidence)[0].RecordedAt = testNow.Add(5 * time.Minute) }},
		{name: "wrong evidence domain", mutate: func(evidence *[]EvidenceReference) { (*evidence)[0].Kind = EvidenceFencingConfirmation }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := append([]EvidenceReference(nil), valid...)
			test.mutate(&evidence)
			request := drillTransition(verifying, RestoreSucceeded, testNow.Add(4*time.Minute))
			request.VerificationEvidence = evidence
			if _, err := ApplyRestoreDrillTransition(registry, verifying, request, "rv:success"); !errors.Is(err, ErrInvalidRestoreDrillTransition) {
				t.Fatalf("error = %v, want ErrInvalidRestoreDrillTransition", err)
			}
		})
	}
}
