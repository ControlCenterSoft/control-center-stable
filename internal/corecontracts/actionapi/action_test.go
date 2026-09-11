package actionapi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"control-center/internal/corecontracts"
	"control-center/internal/identity/rbac"
	"control-center/internal/orchestration/action"
	"control-center/internal/orchestration/job"
	"control-center/internal/orchestration/policy"
	"control-center/internal/orchestration/worker"
)

func TestApplyActionUsesDurableDownstreamIdempotency(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	repository, err := corecontracts.NewMemoryObjectRepository([]corecontracts.StoredObject{corecontracts.LegacyGlobalScopeObject(now)})
	if err != nil {
		t.Fatal(err)
	}
	definition, err := NewApplyAction(repository)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Name != ApplyActionName || definition.Permission != string(rbac.PermissionCoreObjectsWrite) || definition.Risk != policy.RiskHigh {
		t.Fatalf("action definition = %#v", definition)
	}
	input := json.RawMessage(`{"operation":"create","object_type":"desired-state","object_id":"desired-a","scope_id":"global","owner_scope":"global","document":{"kind":"service.config","target_object_id":"service-a","spec":{"enabled":true}}}`)
	ctx := action.WithInvocation(context.Background(), action.Invocation{
		JobID: "job-1", ChangeID: "change-1", ActionName: ApplyActionName,
		IdempotencyKey: "job-idempotency-1", Attempt: 1,
	})
	first, err := definition.Execute(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := definition.Verify(ctx, input, first); err != nil {
		t.Fatal(err)
	}
	replayed, err := definition.Execute(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.ActualStates[0].Revision != replayed.ActualStates[0].Revision {
		t.Fatalf("retry allocated a new resource_version: first=%q retry=%q", first.ActualStates[0].Revision, replayed.ActualStates[0].Revision)
	}

	different := json.RawMessage(`{"operation":"create","object_type":"desired-state","object_id":"desired-b","scope_id":"global","owner_scope":"global","document":{"kind":"service.config","target_object_id":"service-b","spec":{}}}`)
	if _, err := definition.Execute(ctx, different); !errors.Is(err, corecontracts.ErrIdempotencyConflict) {
		t.Fatalf("idempotency conflict error = %v", err)
	}
}

func TestApplyActionSchemaIncludesNetworkContractObjects(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	repository, err := corecontracts.NewMemoryObjectRepository([]corecontracts.StoredObject{corecontracts.LegacyGlobalScopeObject(now)})
	if err != nil {
		t.Fatal(err)
	}
	definition, err := NewApplyAction(repository)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			ObjectType struct {
				Enum []string `json:"enum"`
			} `json:"object_type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(definition.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{"network-zone": false, "network-interface": false}
	for _, objectType := range schema.Properties.ObjectType.Enum {
		if _, exists := wanted[objectType]; exists {
			wanted[objectType] = true
		}
	}
	for objectType, found := range wanted {
		if !found {
			t.Fatalf("core.object.apply schema lacks %q: %s", objectType, definition.InputSchema)
		}
	}
}

func TestApplyActionRequiresWorkerInvocation(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	repository, err := corecontracts.NewMemoryObjectRepository([]corecontracts.StoredObject{corecontracts.LegacyGlobalScopeObject(now)})
	if err != nil {
		t.Fatal(err)
	}
	definition, err := NewApplyAction(repository)
	if err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"operation":"create","object_type":"desired-state","object_id":"desired-a","scope_id":"global","owner_scope":"global","document":{"kind":"service.config","target_object_id":"service-a","spec":{}}}`)
	if _, err := definition.Execute(context.Background(), input); err == nil {
		t.Fatal("action executed without worker invocation context")
	}
}

func TestApplyActionJobRetryReusesTheCommittedCASResult(t *testing.T) {
	base := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	repository, err := corecontracts.NewMemoryObjectRepository([]corecontracts.StoredObject{corecontracts.LegacyGlobalScopeObject(base)})
	if err != nil {
		t.Fatal(err)
	}
	definition, err := NewApplyAction(repository)
	if err != nil {
		t.Fatal(err)
	}
	registry := action.NewRegistry()
	if err := registry.Register(definition); err != nil {
		t.Fatal(err)
	}
	jobs := job.NewMemoryRepository()
	input := json.RawMessage(`{"operation":"create","object_type":"desired-state","object_id":"desired-job-retry","scope_id":"global","owner_scope":"global","document":{"kind":"service.config","target_object_id":"service-a","spec":{"enabled":true}}}`)
	if _, created, err := jobs.Create(context.Background(), job.CreateRequest{
		ID: "job-core-cas", ChangeID: "change-core-cas", ActionName: ApplyActionName,
		Input: input, IdempotencyKey: "change:core-cas", MaxAttempts: 3, Now: base,
	}); err != nil || !created {
		t.Fatalf("create job: created=%v err=%v", created, err)
	}
	failures := &failOnceAfterExecute{}
	runner := worker.Worker{
		ID: "core-test-worker", Repository: jobs, Registry: registry,
		Allowlist: worker.Set(ApplyActionName), Permissions: worker.Set(string(rbac.PermissionCoreObjectsWrite)),
		LeaseTTL: time.Minute, RetryPolicy: job.RetryPolicy{BaseDelay: time.Second}, Failures: failures,
	}
	first, claimed, err := runner.RunOne(context.Background(), base)
	if !claimed || err == nil || first.Status != job.StatusRetryWait {
		t.Fatalf("first attempt: claimed=%v status=%s err=%v", claimed, first.Status, err)
	}
	storedAfterFailure, err := repository.Get(context.Background(), "desired-job-retry")
	if err != nil {
		t.Fatal(err)
	}

	second, claimed, err := runner.RunOne(context.Background(), base.Add(2*time.Second))
	if err != nil || !claimed || second.Status != job.StatusSucceeded || second.Attempt != 2 {
		t.Fatalf("retry: claimed=%v status=%s attempt=%d err=%v", claimed, second.Status, second.Attempt, err)
	}
	storedAfterRetry, err := repository.Get(context.Background(), "desired-job-retry")
	if err != nil {
		t.Fatal(err)
	}
	if storedAfterRetry.ResourceVersion != storedAfterFailure.ResourceVersion ||
		storedAfterRetry.Generation != storedAfterFailure.Generation ||
		!storedAfterRetry.UpdatedAt.Equal(storedAfterFailure.UpdatedAt) {
		t.Fatalf("Job retry repeated the CAS write: first=%#v retry=%#v", storedAfterFailure.ObjectMetadata, storedAfterRetry.ObjectMetadata)
	}
}

type failOnceAfterExecute struct {
	failed bool
}

func (f *failOnceAfterExecute) Inject(_ context.Context, point worker.FailurePoint, _ job.Job) error {
	if point == worker.FailureAfterExecute && !f.failed {
		f.failed = true
		return errors.New("simulated worker crash after persistence")
	}
	return nil
}
