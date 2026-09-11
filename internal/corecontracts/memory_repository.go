package corecontracts

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// MemoryObjectRepository implements the same validated CAS boundary used by
// PostgreSQL. It is intended for tests and explicit in-memory deployments.
type MemoryObjectRepository struct {
	mu         sync.RWMutex
	objects    map[string]StoredObject
	receipts   map[string]mutationReceipt
	now        func() time.Time
	newVersion func() (string, error)
}

type mutationReceipt struct {
	fingerprint string
	result      StoredObject
}

// NewMemoryObjectRepository returns an isolated repository after validating
// the complete initial snapshot.
func NewMemoryObjectRepository(initial []StoredObject) (*MemoryObjectRepository, error) {
	if err := ValidateStoredObjects(initial); err != nil {
		return nil, err
	}
	repository := &MemoryObjectRepository{
		objects:    make(map[string]StoredObject, len(initial)),
		receipts:   make(map[string]mutationReceipt),
		now:        time.Now,
		newVersion: NewOpaqueResourceVersion,
	}
	for _, object := range initial {
		repository.objects[object.ObjectID] = cloneStoredObject(object)
	}
	return repository, nil
}

func (r *MemoryObjectRepository) List(ctx context.Context, filter ObjectFilter) ([]StoredObject, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if filter.ObjectType != "" && !filter.ObjectType.Valid() {
		return nil, fmt.Errorf("%w: unsupported object_type %q", ErrInvalidObject, filter.ObjectType)
	}
	for _, identifier := range []string{filter.ScopeID, filter.OwnerScope} {
		if identifier != "" {
			if err := ValidateObjectIdentifier(identifier); err != nil {
				return nil, err
			}
		}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]StoredObject, 0, len(r.objects))
	for _, object := range r.objects {
		if filter.ObjectType != "" && object.ObjectType != filter.ObjectType {
			continue
		}
		if filter.ScopeID != "" && object.ScopeID != filter.ScopeID {
			continue
		}
		if filter.OwnerScope != "" && object.OwnerScope != filter.OwnerScope {
			continue
		}
		result = append(result, cloneStoredObject(object))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ObjectType == result[j].ObjectType {
			return result[i].ObjectID < result[j].ObjectID
		}
		return result[i].ObjectType < result[j].ObjectType
	})
	return result, nil
}

func (r *MemoryObjectRepository) Get(ctx context.Context, objectID string) (StoredObject, error) {
	if err := ctx.Err(); err != nil {
		return StoredObject{}, err
	}
	if err := ValidateObjectIdentifier(objectID); err != nil {
		return StoredObject{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	object, exists := r.objects[objectID]
	if !exists {
		return StoredObject{}, ErrObjectNotFound
	}
	return cloneStoredObject(object), nil
}

func (r *MemoryObjectRepository) Apply(ctx context.Context, request MutationRequest, idempotencyKey string) (StoredObject, error) {
	if err := ctx.Err(); err != nil {
		return StoredObject{}, err
	}
	if err := ValidateIdempotencyKey(idempotencyKey); err != nil {
		return StoredObject{}, err
	}
	fingerprint, err := MutationFingerprint(request)
	if err != nil {
		return StoredObject{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if receipt, exists := r.receipts[idempotencyKey]; exists {
		if receipt.fingerprint != fingerprint {
			return StoredObject{}, fmt.Errorf("%w: key %q was used for another request", ErrIdempotencyConflict, idempotencyKey)
		}
		return cloneStoredObject(receipt.result), nil
	}
	var current *StoredObject
	if object, exists := r.objects[request.ObjectID]; exists {
		copyOfObject := cloneStoredObject(object)
		current = &copyOfObject
	}
	version, err := r.newVersion()
	if err != nil {
		return StoredObject{}, err
	}
	next, err := PrepareMutation(current, request, r.snapshotLocked(), r.now().UTC(), version)
	if err != nil {
		return StoredObject{}, err
	}
	r.objects[next.ObjectID] = cloneStoredObject(next)
	r.receipts[idempotencyKey] = mutationReceipt{fingerprint: fingerprint, result: cloneStoredObject(next)}
	return cloneStoredObject(next), nil
}

func (r *MemoryObjectRepository) snapshotLocked() []StoredObject {
	result := make([]StoredObject, 0, len(r.objects))
	for _, object := range r.objects {
		result = append(result, cloneStoredObject(object))
	}
	return result
}
