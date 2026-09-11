package corecontracts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoryObjectRepositoryPersistsTypedCoreObjects(t *testing.T) {
	repository := newDeterministicObjectRepository(t)
	ctx := context.Background()

	scope := applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectScope,
		ObjectID: "scope-site-a", ScopeID: "global", OwnerScope: "global",
		Document: json.RawMessage(`{"id":"scope-site-a","kind":"site","name":"Site A scope","parent_id":"global","delegated_authorities":["desired-state","rbac"]}`),
	})
	if scope.Generation != 1 || scope.ResourceVersion != "rv-test-01" {
		t.Fatalf("scope metadata = %#v", scope.ObjectMetadata)
	}
	applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectSite,
		ObjectID: "site-a", ScopeID: "scope-site-a", OwnerScope: "global",
		Document: json.RawMessage(`{"id":"site-a","name":"Site A","scope_id":"scope-site-a"}`),
	})
	applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectManagementZone,
		ObjectID: "zone-a", ScopeID: "scope-site-a", OwnerScope: "global",
		Document: json.RawMessage(`{"id":"zone-a","name":"Site A management","scope_id":"scope-site-a","site_id":"site-a"}`),
	})
	applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectRoleAssignment,
		ObjectID: "role-worker-a", ScopeID: "scope-site-a", OwnerScope: "global",
		Document: json.RawMessage(`{"target_node_id":"node-a","service_identity_id":"worker-a","role":"worker-node","site_id":"site-a","management_zone_id":"zone-a"}`),
	})
	desired := applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectDesiredState,
		ObjectID: "desired-node-a", ScopeID: "scope-site-a", OwnerScope: "global",
		Document: json.RawMessage(`{"kind":"node.configuration","target_object_id":"node-a","spec":{"enabled":true}}`),
	})
	actual := applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectActualState,
		ObjectID: "actual-node-a", ScopeID: "scope-site-a", OwnerScope: "scope-site-a",
		Document: json.RawMessage(`{"kind":"node.configuration","target_object_id":"node-a","desired_object_id":"desired-node-a","observed_generation":1,"source_node_id":"node-a","observed_at":"2026-09-08T12:00:00Z","status":"progressing","state":{"enabled":false}}`),
	})

	items, err := repository.List(ctx, ObjectFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 7 { // deterministic legacy global root plus six created objects
		t.Fatalf("List() count = %d, want 7", len(items))
	}
	for index := 1; index < len(items); index++ {
		left, right := items[index-1], items[index]
		if left.ObjectType > right.ObjectType || (left.ObjectType == right.ObjectType && left.ObjectID > right.ObjectID) {
			t.Fatalf("List() is not deterministic: %#v", items)
		}
	}

	updatedDesired := applyObject(t, repository, MutationRequest{
		Operation: MutationReplace, ObjectType: ObjectDesiredState,
		ObjectID: desired.ObjectID, ScopeID: desired.ScopeID, OwnerScope: desired.OwnerScope,
		Document:     json.RawMessage(`{ "target_object_id":"node-a", "kind":"node.configuration", "spec":{"enabled":false} }`),
		Precondition: objectPrecondition(desired),
	})
	if updatedDesired.Generation != 2 || updatedDesired.ResourceVersion == desired.ResourceVersion {
		t.Fatalf("updated Desired metadata = %#v", updatedDesired.ObjectMetadata)
	}

	updatedActual := applyObject(t, repository, MutationRequest{
		Operation: MutationReplace, ObjectType: ObjectActualState,
		ObjectID: actual.ObjectID, ScopeID: actual.ScopeID, OwnerScope: actual.OwnerScope,
		Document:     json.RawMessage(`{"kind":"node.configuration","target_object_id":"node-a","desired_object_id":"desired-node-a","observed_generation":2,"source_node_id":"node-a","observed_at":"2026-09-08T12:01:00Z","status":"converged","state":{"enabled":false}}`),
		Precondition: objectPrecondition(actual),
	})
	if updatedActual.Generation != actual.Generation || updatedActual.ResourceVersion == actual.ResourceVersion {
		t.Fatalf("Actual observation changed semantic generation: before=%#v after=%#v", actual.ObjectMetadata, updatedActual.ObjectMetadata)
	}
}

func TestMemoryObjectRepositoryFailsClosedOnCASConflicts(t *testing.T) {
	repository := newDeterministicObjectRepository(t)
	created := applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectDesiredState,
		ObjectID: "desired-a", ScopeID: "global", OwnerScope: "global",
		Document: json.RawMessage(`{"kind":"service.config","target_object_id":"service-a","spec":{"value":1}}`),
	})
	replace := MutationRequest{
		Operation: MutationReplace, ObjectType: ObjectDesiredState,
		ObjectID: created.ObjectID, ScopeID: created.ScopeID, OwnerScope: created.OwnerScope,
		Document: json.RawMessage(`{"kind":"service.config","target_object_id":"service-a","spec":{"value":2}}`),
	}

	if _, err := repository.Apply(context.Background(), replace, nextMutationKey()); !errors.Is(err, ErrPreconditionRequired) {
		t.Fatalf("missing precondition error = %v, want ErrPreconditionRequired", err)
	}
	replace.Precondition = &ObjectPrecondition{ObjectID: created.ObjectID, ResourceVersion: "bad version"}
	if _, err := repository.Apply(context.Background(), replace, nextMutationKey()); !errors.Is(err, ErrInvalidPrecondition) {
		t.Fatalf("malformed precondition error = %v, want ErrInvalidPrecondition", err)
	}
	replace.Precondition = &ObjectPrecondition{ObjectID: created.ObjectID, ResourceVersion: "rv-stale"}
	if _, err := repository.Apply(context.Background(), replace, nextMutationKey()); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("stale precondition error = %v, want ErrPreconditionFailed", err)
	}

	unchanged, err := repository.Get(context.Background(), created.ObjectID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.ResourceVersion != created.ResourceVersion || unchanged.Generation != created.Generation || string(unchanged.Document) != string(created.Document) {
		t.Fatalf("failed mutations changed object: %#v", unchanged)
	}
}

func TestMemoryObjectRepositoryRejectsDuplicateAndInvalidTopology(t *testing.T) {
	repository := newDeterministicObjectRepository(t)
	request := MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectScope,
		ObjectID: "scope-a", ScopeID: "missing-parent", OwnerScope: "global",
		Document: json.RawMessage(`{"id":"scope-a","kind":"site","name":"A","parent_id":"missing-parent"}`),
	}
	if _, err := repository.Apply(context.Background(), request, nextMutationKey()); !errors.Is(err, ErrInvalidObject) {
		t.Fatalf("invalid topology error = %v, want ErrInvalidObject", err)
	}

	request = MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectDesiredState,
		ObjectID: "desired-a", ScopeID: "global", OwnerScope: "global",
		Document: json.RawMessage(`{"kind":"service.config","target_object_id":"service-a","spec":{}}`),
	}
	applyObject(t, repository, request)
	if _, err := repository.Apply(context.Background(), request, nextMutationKey()); !errors.Is(err, ErrObjectAlreadyExists) {
		t.Fatalf("duplicate create error = %v, want ErrObjectAlreadyExists", err)
	}
}

func TestMemoryObjectRepositoryRejectsIndependentControllersInOneScope(t *testing.T) {
	repository := newDeterministicObjectRepository(t)
	applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectRoleAssignment,
		ObjectID: "global-controller-a", ScopeID: "global", OwnerScope: "global",
		Document: json.RawMessage(`{"target_node_id":"node-a","service_identity_id":"global-control-plane","role":"global-controller"}`),
	})

	_, err := repository.Apply(context.Background(), MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectRoleAssignment,
		ObjectID: "global-controller-b", ScopeID: "global", OwnerScope: "global",
		Document: json.RawMessage(`{"target_node_id":"node-b","service_identity_id":"independent-control-plane","role":"controller-cluster-member"}`),
	}, nextMutationKey())
	if !errors.Is(err, ErrInvalidObject) || !errors.Is(err, ErrInvalidRoleAssignment) {
		t.Fatalf("independent controller error = %v, want ErrInvalidObject and ErrInvalidRoleAssignment", err)
	}
	if _, getErr := repository.Get(context.Background(), "global-controller-b"); !errors.Is(getErr, ErrObjectNotFound) {
		t.Fatalf("rejected controller was persisted: %v", getErr)
	}
}

func TestPrepareMutationTreatsEquivalentJSONAsNoSemanticChange(t *testing.T) {
	repository := newDeterministicObjectRepository(t)
	created := applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectDesiredState,
		ObjectID: "desired-a", ScopeID: "global", OwnerScope: "global",
		Document: json.RawMessage(`{"kind":"service.config","target_object_id":"service-a","spec":{"a":1,"b":2}}`),
	})
	replaced := applyObject(t, repository, MutationRequest{
		Operation: MutationReplace, ObjectType: ObjectDesiredState,
		ObjectID: created.ObjectID, ScopeID: created.ScopeID, OwnerScope: created.OwnerScope,
		Document: json.RawMessage(`{
            "spec":{"b":2,"a":1}, "target_object_id":"service-a", "kind":"service.config"
        }`),
		Precondition: objectPrecondition(created),
	})
	if replaced.Generation != created.Generation {
		t.Fatalf("equivalent JSON advanced generation: before=%d after=%d", created.Generation, replaced.Generation)
	}
}

func TestActualStateReplacementRejectsObservationRegression(t *testing.T) {
	repository := newDeterministicObjectRepository(t)
	created := applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectActualState,
		ObjectID: "actual-a", ScopeID: "global", OwnerScope: "global",
		Document: json.RawMessage(`{"kind":"service.config","target_object_id":"service-a","desired_object_id":"desired-a","observed_generation":2,"source_node_id":"node-a","observed_at":"2026-09-08T12:00:00Z","status":"converged","state":{}}`),
	})
	request := MutationRequest{
		Operation: MutationReplace, ObjectType: ObjectActualState,
		ObjectID: created.ObjectID, ScopeID: created.ScopeID, OwnerScope: created.OwnerScope,
		Document:     json.RawMessage(`{"kind":"service.config","target_object_id":"service-a","desired_object_id":"desired-a","observed_generation":1,"source_node_id":"node-a","observed_at":"2026-09-08T11:59:00Z","status":"progressing","state":{}}`),
		Precondition: objectPrecondition(created),
	}
	if _, err := repository.Apply(context.Background(), request, nextMutationKey()); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("regression error = %v, want ErrInvalidTransition", err)
	}
}

func TestStoredObjectDocumentIsDefensivelyCopied(t *testing.T) {
	repository := newDeterministicObjectRepository(t)
	created := applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectDesiredState,
		ObjectID: "desired-a", ScopeID: "global", OwnerScope: "global",
		Document: json.RawMessage(`{"kind":"service.config","target_object_id":"service-a","spec":{}}`),
	})
	created.Document[0] = '['
	loaded, err := repository.Get(context.Background(), "desired-a")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Document[0] != '{' {
		t.Fatalf("repository object mutated through returned slice: %s", loaded.Document)
	}
}

func TestMemoryObjectRepositoryReplaysIdempotentMutation(t *testing.T) {
	repository := newDeterministicObjectRepository(t)
	request := MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectDesiredState,
		ObjectID: "desired-a", ScopeID: "global", OwnerScope: "global",
		Document: json.RawMessage(`{"kind":"service.config","target_object_id":"service-a","spec":{"a":1,"b":2}}`),
	}
	first, err := repository.Apply(context.Background(), request, "job-key-1")
	if err != nil {
		t.Fatal(err)
	}
	request.Document = json.RawMessage(`{ "spec":{"b":2,"a":1}, "target_object_id":"service-a", "kind":"service.config" }`)
	replayed, err := repository.Apply(context.Background(), request, "job-key-1")
	if err != nil {
		t.Fatal(err)
	}
	if first.ResourceVersion != replayed.ResourceVersion || !first.UpdatedAt.Equal(replayed.UpdatedAt) {
		t.Fatalf("replay allocated a new version: first=%#v replayed=%#v", first.ObjectMetadata, replayed.ObjectMetadata)
	}

	request.ObjectID = "desired-b"
	if _, err := repository.Apply(context.Background(), request, "job-key-1"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("idempotency key reuse error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestMemoryObjectRepositoryRejectsMissingIdempotencyKey(t *testing.T) {
	repository := newDeterministicObjectRepository(t)
	_, err := repository.Apply(context.Background(), MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectDesiredState,
		ObjectID: "desired-a", ScopeID: "global", OwnerScope: "global",
		Document: json.RawMessage(`{"kind":"service.config","target_object_id":"service-a","spec":{}}`),
	}, "")
	if !errors.Is(err, ErrInvalidMutation) {
		t.Fatalf("missing idempotency key error = %v, want ErrInvalidMutation", err)
	}
}

func TestMemoryObjectRepositoryRejectsMalformedReadIdentifiers(t *testing.T) {
	repository := newDeterministicObjectRepository(t)
	if _, err := repository.Get(context.Background(), "bad id"); !errors.Is(err, ErrInvalidObject) {
		t.Fatalf("malformed Get identifier error = %v, want ErrInvalidObject", err)
	}
	if _, err := repository.List(context.Background(), ObjectFilter{ScopeID: "bad id"}); !errors.Is(err, ErrInvalidObject) {
		t.Fatalf("malformed List scope error = %v, want ErrInvalidObject", err)
	}
}

func newDeterministicObjectRepository(t *testing.T) *MemoryObjectRepository {
	t.Helper()
	base := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	repository, err := NewMemoryObjectRepository([]StoredObject{LegacyGlobalScopeObject(base)})
	if err != nil {
		t.Fatal(err)
	}
	sequence := 0
	repository.now = func() time.Time {
		sequence++
		return base.Add(time.Duration(sequence) * time.Minute)
	}
	repository.newVersion = func() (string, error) {
		return fmt.Sprintf("rv-test-%02d", sequence+1), nil
	}
	return repository
}

func applyObject(t *testing.T, repository ObjectRepository, request MutationRequest) StoredObject {
	t.Helper()
	object, err := repository.Apply(context.Background(), request, nextMutationKey())
	if err != nil {
		t.Fatalf("Apply(%s %s) error = %v", request.Operation, request.ObjectID, err)
	}
	return object
}

var mutationKeySequence uint64

func nextMutationKey() string {
	return fmt.Sprintf("test-mutation-%d", atomic.AddUint64(&mutationKeySequence, 1))
}

func objectPrecondition(object StoredObject) *ObjectPrecondition {
	generation := object.Generation
	return &ObjectPrecondition{ObjectID: object.ObjectID, ResourceVersion: object.ResourceVersion, Generation: &generation}
}
