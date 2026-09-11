package corecontracts

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestObjectMetadataValidate(t *testing.T) {
	metadata := testMetadata("object-1", "site-a", "global", 3, "rv:42")
	if err := metadata.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*ObjectMetadata)
	}{
		{name: "missing object", mutate: func(m *ObjectMetadata) { m.ObjectID = "" }},
		{name: "whitespace in scope", mutate: func(m *ObjectMetadata) { m.ScopeID = "site a" }},
		{name: "path separator in object", mutate: func(m *ObjectMetadata) { m.ObjectID = "objects/one" }},
		{name: "missing owner", mutate: func(m *ObjectMetadata) { m.OwnerScope = "" }},
		{name: "zero generation", mutate: func(m *ObjectMetadata) { m.Generation = 0 }},
		{name: "missing version", mutate: func(m *ObjectMetadata) { m.ResourceVersion = "" }},
		{name: "version whitespace", mutate: func(m *ObjectMetadata) { m.ResourceVersion = "rv 42" }},
		{name: "missing creation time", mutate: func(m *ObjectMetadata) { m.CreatedAt = time.Time{} }},
		{name: "updated before created", mutate: func(m *ObjectMetadata) { m.UpdatedAt = m.CreatedAt.Add(-time.Second) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := metadata
			test.mutate(&candidate)
			if err := candidate.Validate(); !errors.Is(err, ErrInvalidMetadata) {
				t.Fatalf("Validate() error = %v, want ErrInvalidMetadata", err)
			}
		})
	}
}

func TestObjectPreconditionValidateAgainst(t *testing.T) {
	metadata := testMetadata("object-1", "site-a", "global", 3, "opaque-version:42")
	generation := metadata.Generation
	if err := (ObjectPrecondition{
		ObjectID:        metadata.ObjectID,
		ResourceVersion: metadata.ResourceVersion,
		Generation:      &generation,
	}).ValidateAgainst(metadata); err != nil {
		t.Fatalf("ValidateAgainst() error = %v", err)
	}

	tests := []struct {
		name string
		in   ObjectPrecondition
		want error
	}{
		{name: "missing", in: ObjectPrecondition{}, want: ErrPreconditionRequired},
		{name: "missing version", in: ObjectPrecondition{ObjectID: metadata.ObjectID}, want: ErrPreconditionRequired},
		{name: "wrong object", in: ObjectPrecondition{ObjectID: "object-2", ResourceVersion: metadata.ResourceVersion}, want: ErrPreconditionFailed},
		{name: "stale version", in: ObjectPrecondition{ObjectID: metadata.ObjectID, ResourceVersion: "rv:41"}, want: ErrPreconditionFailed},
		{name: "stale generation", in: ObjectPrecondition{ObjectID: metadata.ObjectID, ResourceVersion: metadata.ResourceVersion, Generation: uint64Pointer(2)}, want: ErrPreconditionFailed},
		{name: "malformed object", in: ObjectPrecondition{ObjectID: "objects/one", ResourceVersion: metadata.ResourceVersion}, want: ErrInvalidPrecondition},
		{name: "malformed version", in: ObjectPrecondition{ObjectID: metadata.ObjectID, ResourceVersion: "rv 42"}, want: ErrInvalidPrecondition},
		{name: "zero generation", in: ObjectPrecondition{ObjectID: metadata.ObjectID, ResourceVersion: metadata.ResourceVersion, Generation: uint64Pointer(0)}, want: ErrInvalidPrecondition},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.in.ValidateAgainst(metadata); !errors.Is(err, test.want) {
				t.Fatalf("ValidateAgainst() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestValidateSuccessor(t *testing.T) {
	current := testMetadata("object-1", "site-a", "global", 7, "rv:7")
	nextObservation := current
	// Resource versions are opaque: a lexically/numerically smaller token is a
	// valid successor as long as it is different.
	nextObservation.ResourceVersion = "rv:1"
	nextObservation.UpdatedAt = current.UpdatedAt.Add(time.Second)
	if err := ValidateSuccessor(current, nextObservation, false); err != nil {
		t.Fatalf("observation successor error = %v", err)
	}

	nextDesired := nextObservation
	nextDesired.Generation++
	if err := ValidateSuccessor(current, nextDesired, true); err != nil {
		t.Fatalf("desired successor error = %v", err)
	}

	tests := []struct {
		name           string
		candidate      ObjectMetadata
		desiredChanged bool
	}{
		{name: "object changed", candidate: mutateMetadata(nextObservation, func(m *ObjectMetadata) { m.ObjectID = "object-2" })},
		{name: "creation changed", candidate: mutateMetadata(nextObservation, func(m *ObjectMetadata) { m.CreatedAt = m.CreatedAt.Add(time.Second) })},
		{name: "time backwards", candidate: mutateMetadata(nextObservation, func(m *ObjectMetadata) { m.UpdatedAt = current.UpdatedAt.Add(-time.Second) })},
		{name: "version reused", candidate: mutateMetadata(nextObservation, func(m *ObjectMetadata) { m.ResourceVersion = current.ResourceVersion })},
		{name: "scope moved without desired change", candidate: mutateMetadata(nextObservation, func(m *ObjectMetadata) { m.ScopeID = "site-b" })},
		{name: "unexpected generation bump", candidate: nextDesired},
		{name: "missing generation bump", candidate: nextObservation, desiredChanged: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateSuccessor(current, test.candidate, test.desiredChanged); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("ValidateSuccessor() error = %v, want ErrInvalidTransition", err)
			}
		})
	}

	overflow := current
	overflow.Generation = math.MaxUint64
	overflow.ResourceVersion = "rv:max"
	overflowNext := overflow
	overflowNext.ResourceVersion = "rv:overflow"
	if err := ValidateSuccessor(overflow, overflowNext, true); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("overflow error = %v, want ErrInvalidTransition", err)
	}
}

func testMetadata(objectID, scopeID, ownerScope string, generation uint64, resourceVersion string) ObjectMetadata {
	created := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	return ObjectMetadata{
		ObjectID:        objectID,
		ScopeID:         scopeID,
		OwnerScope:      ownerScope,
		Generation:      generation,
		ResourceVersion: resourceVersion,
		CreatedAt:       created,
		UpdatedAt:       created.Add(time.Minute),
	}
}

func uint64Pointer(value uint64) *uint64 { return &value }

func mutateMetadata(source ObjectMetadata, mutate func(*ObjectMetadata)) ObjectMetadata {
	mutate(&source)
	return source
}
