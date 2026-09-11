package corecontracts

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestDesiredAndActualStateOwnershipAndConvergence(t *testing.T) {
	topology := testTopology(t)
	desired, actual := testStatePair()
	if err := ValidateDesiredState(desired, topology); err != nil {
		t.Fatalf("ValidateDesiredState() error = %v", err)
	}
	localOverride := desired
	localOverride.OwnerScope = "site-a"
	if err := ValidateDesiredState(localOverride, topology); err != nil {
		t.Fatalf("explicitly delegated local Desired State rejected: %v", err)
	}
	if err := ValidateActualState(actual, topology); err != nil {
		t.Fatalf("ValidateActualState() error = %v", err)
	}
	if err := ValidateStatePair(desired, actual, topology); err != nil {
		t.Fatalf("ValidateStatePair() error = %v", err)
	}
	historical := actual
	historical.ObservedAt = historical.CreatedAt.Add(-time.Hour)
	if err := ValidateActualState(historical, topology); err != nil {
		t.Fatalf("historical observation rejected: %v", err)
	}
	converged, err := IsConverged(desired, actual, topology)
	if err != nil || !converged {
		t.Fatalf("IsConverged() = %v, %v, want true, nil", converged, err)
	}

	actual.ObservedGeneration--
	converged, err = IsConverged(desired, actual, topology)
	if err != nil || converged {
		t.Fatalf("lagging IsConverged() = %v, %v, want false, nil", converged, err)
	}
}

func TestValidateDesiredStateRejectsBottomUpOwnershipAndUnsafePayloads(t *testing.T) {
	topology := testTopology(t)
	desired, _ := testStatePair()
	tests := []struct {
		name   string
		mutate func(*DesiredState)
	}{
		{name: "owner below target", mutate: func(s *DesiredState) { s.OwnerScope = "site-a-resources" }},
		{name: "owner lacks delegation", mutate: func(s *DesiredState) { s.OwnerScope = "region-a" }},
		{name: "unknown target scope", mutate: func(s *DesiredState) { s.ScopeID = "missing" }},
		{name: "missing target", mutate: func(s *DesiredState) { s.TargetObjectID = "" }},
		{name: "missing kind", mutate: func(s *DesiredState) { s.Kind = "" }},
		{name: "invalid json", mutate: func(s *DesiredState) { s.Spec = json.RawMessage(`{"broken"`) }},
		{name: "scalar payload", mutate: func(s *DesiredState) { s.Spec = json.RawMessage(`"shell command"`) }},
		{name: "null payload", mutate: func(s *DesiredState) { s.Spec = json.RawMessage(`null`) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := desired
			test.mutate(&candidate)
			if err := ValidateDesiredState(candidate, topology); !errors.Is(err, ErrInvalidDesiredState) {
				t.Fatalf("ValidateDesiredState() error = %v, want ErrInvalidDesiredState", err)
			}
		})
	}
}

func TestValidateActualStateRejectsTopDownOwnershipAndInvalidObservations(t *testing.T) {
	topology := testTopology(t)
	_, actual := testStatePair()
	tests := []struct {
		name   string
		mutate func(*ActualState)
	}{
		{name: "owner below target", mutate: func(s *ActualState) {
			s.ScopeID = "site-a"
			s.OwnerScope = "site-a-resources"
		}},
		{name: "unknown target scope", mutate: func(s *ActualState) { s.ScopeID = "missing" }},
		{name: "missing desired identity", mutate: func(s *ActualState) { s.DesiredObjectID = "" }},
		{name: "missing source node", mutate: func(s *ActualState) { s.SourceNodeID = "" }},
		{name: "unknown status", mutate: func(s *ActualState) { s.Status = "successful" }},
		{name: "missing observed time", mutate: func(s *ActualState) { s.ObservedAt = time.Time{} }},
		{name: "array payload", mutate: func(s *ActualState) { s.State = json.RawMessage(`[]`) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := actual
			test.mutate(&candidate)
			if err := ValidateActualState(candidate, topology); !errors.Is(err, ErrInvalidActualState) {
				t.Fatalf("ValidateActualState() error = %v, want ErrInvalidActualState", err)
			}
		})
	}
}

func TestValidateStatePairDetectsSynchronizationConflicts(t *testing.T) {
	topology := testTopology(t)
	desired, actual := testStatePair()
	tests := []struct {
		name   string
		mutate func(*ActualState)
	}{
		{name: "wrong desired object", mutate: func(s *ActualState) { s.DesiredObjectID = "desired-other" }},
		{name: "wrong target", mutate: func(s *ActualState) { s.TargetObjectID = "node-other" }},
		{name: "wrong kind", mutate: func(s *ActualState) { s.Kind = "service.role" }},
		{name: "future generation", mutate: func(s *ActualState) { s.ObservedGeneration = desired.Generation + 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := actual
			test.mutate(&candidate)
			if err := ValidateStatePair(desired, candidate, topology); !errors.Is(err, ErrStateConflict) {
				t.Fatalf("ValidateStatePair() error = %v, want ErrStateConflict", err)
			}
		})
	}
}

func testStatePair() (DesiredState, ActualState) {
	desiredMetadata := testMetadata("desired-1", "site-a", "global", 4, "rv:desired-4")
	desired := DesiredState{
		ObjectMetadata: desiredMetadata,
		Kind:           "node.role-assignment",
		TargetObjectID: "node-1",
		Spec:           json.RawMessage(`{"roles":["worker-node"]}`),
	}
	actualMetadata := testMetadata("actual-1", "site-a-resources", "site-a", 9, "rv:actual-9")
	actual := ActualState{
		ObjectMetadata:     actualMetadata,
		Kind:               desired.Kind,
		TargetObjectID:     desired.TargetObjectID,
		DesiredObjectID:    desired.ObjectID,
		ObservedGeneration: desired.Generation,
		SourceNodeID:       "node-1",
		ObservedAt:         actualMetadata.UpdatedAt.Add(time.Second),
		Status:             ActualStateConverged,
		State:              json.RawMessage(`{"roles":["worker-node"],"healthy":true}`),
	}
	return desired, actual
}
