package corecontracts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidDesiredState = errors.New("invalid desired state")
	ErrInvalidActualState  = errors.New("invalid actual state")
	ErrStateConflict       = errors.New("desired/actual state conflict")
)

// ActualStateStatus is the producer's typed assessment of convergence.
type ActualStateStatus string

const (
	ActualStateConverged   ActualStateStatus = "converged"
	ActualStateProgressing ActualStateStatus = "progressing"
	ActualStateDrifted     ActualStateStatus = "drifted"
	ActualStateFailed      ActualStateStatus = "failed"
	ActualStateUnknown     ActualStateStatus = "unknown"
)

// DesiredState is top-down intent. OwnerScope must be the target ScopeID or
// one of its ancestors. Spec is a versioned domain object, not an executable
// command or provider-specific shell payload.
type DesiredState struct {
	ObjectMetadata
	Kind           string          `json:"kind"`
	TargetObjectID string          `json:"target_object_id"`
	Spec           json.RawMessage `json:"spec"`
}

// ActualState is a bottom-up observation kept separately from DesiredState.
// OwnerScope is the local reporting scope and must contain ScopeID. Transport
// synchronizes the observation to ancestors without rewriting its ownership.
type ActualState struct {
	ObjectMetadata
	Kind               string            `json:"kind"`
	TargetObjectID     string            `json:"target_object_id"`
	DesiredObjectID    string            `json:"desired_object_id"`
	ObservedGeneration uint64            `json:"observed_generation"`
	SourceNodeID       string            `json:"source_node_id"`
	ObservedAt         time.Time         `json:"observed_at"`
	Status             ActualStateStatus `json:"status"`
	State              json.RawMessage   `json:"state"`
}

// ValidateDesiredState enforces top-down ownership against the scope graph.
func ValidateDesiredState(state DesiredState, topology Topology) error {
	if err := state.ObjectMetadata.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidDesiredState, err)
	}
	if _, exists := topology.Scope(state.ScopeID); !exists {
		return fmt.Errorf("%w: unknown target scope %q", ErrInvalidDesiredState, state.ScopeID)
	}
	if !topology.Contains(state.OwnerScope, state.ScopeID) {
		return fmt.Errorf("%w: owner scope %q is not the target or its ancestor", ErrInvalidDesiredState, state.OwnerScope)
	}
	if state.OwnerScope != topology.RootID() && !topology.IsDelegated(state.OwnerScope, DelegateDesiredState) {
		return fmt.Errorf("%w: owner scope %q has no desired-state delegation", ErrInvalidDesiredState, state.OwnerScope)
	}
	if err := validateIdentifier("target_object_id", state.TargetObjectID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidDesiredState, err)
	}
	if err := validateStateKind(state.Kind); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidDesiredState, err)
	}
	if err := validateJSONObject("spec", state.Spec); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidDesiredState, err)
	}
	return nil
}

// ValidateActualState enforces bottom-up ownership against the scope graph.
func ValidateActualState(state ActualState, topology Topology) error {
	if err := state.ObjectMetadata.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidActualState, err)
	}
	if _, exists := topology.Scope(state.ScopeID); !exists {
		return fmt.Errorf("%w: unknown target scope %q", ErrInvalidActualState, state.ScopeID)
	}
	if !topology.Contains(state.OwnerScope, state.ScopeID) {
		return fmt.Errorf("%w: owner scope %q does not contain target scope %q", ErrInvalidActualState, state.OwnerScope, state.ScopeID)
	}
	if err := validateIdentifier("target_object_id", state.TargetObjectID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidActualState, err)
	}
	if err := validateIdentifier("desired_object_id", state.DesiredObjectID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidActualState, err)
	}
	if err := validateIdentifier("source_node_id", state.SourceNodeID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidActualState, err)
	}
	if err := validateStateKind(state.Kind); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidActualState, err)
	}
	if !validActualStateStatus(state.Status) {
		return fmt.Errorf("%w: unsupported status %q", ErrInvalidActualState, state.Status)
	}
	if state.ObservedAt.IsZero() {
		return fmt.Errorf("%w: observed_at is required", ErrInvalidActualState)
	}
	if err := validateJSONObject("state", state.State); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidActualState, err)
	}
	return nil
}

// ValidateStatePair verifies that an observation refers to the supplied
// desired object. An older observed generation is valid and represents lag;
// a future generation is an explicit synchronization conflict.
func ValidateStatePair(desired DesiredState, actual ActualState, topology Topology) error {
	if err := ValidateDesiredState(desired, topology); err != nil {
		return err
	}
	if err := ValidateActualState(actual, topology); err != nil {
		return err
	}
	if actual.DesiredObjectID != desired.ObjectID {
		return fmt.Errorf("%w: actual state references desired object %q, want %q", ErrStateConflict, actual.DesiredObjectID, desired.ObjectID)
	}
	if actual.TargetObjectID != desired.TargetObjectID || actual.Kind != desired.Kind {
		return fmt.Errorf("%w: desired and actual targets differ", ErrStateConflict)
	}
	if !topology.Contains(desired.ScopeID, actual.OwnerScope) {
		return fmt.Errorf("%w: actual owner scope %q is outside desired scope %q", ErrStateConflict, actual.OwnerScope, desired.ScopeID)
	}
	if actual.ObservedGeneration > desired.Generation {
		return fmt.Errorf("%w: observed generation %d is newer than desired generation %d", ErrStateConflict, actual.ObservedGeneration, desired.Generation)
	}
	return nil
}

// IsConverged reports whether a valid pair is observed at the current desired
// generation with an explicit converged status.
func IsConverged(desired DesiredState, actual ActualState, topology Topology) (bool, error) {
	if err := ValidateStatePair(desired, actual, topology); err != nil {
		return false, err
	}
	return actual.ObservedGeneration == desired.Generation && actual.Status == ActualStateConverged, nil
}

func validActualStateStatus(status ActualStateStatus) bool {
	switch status {
	case ActualStateConverged, ActualStateProgressing, ActualStateDrifted, ActualStateFailed, ActualStateUnknown:
		return true
	default:
		return false
	}
}

func validateStateKind(value string) error {
	if err := validateIdentifier("kind", value); err != nil {
		return err
	}
	return nil
}

func validateJSONObject(field string, value json.RawMessage) error {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || !json.Valid(trimmed) {
		return fmt.Errorf("%s must be valid JSON", field)
	}
	if trimmed[0] != '{' {
		return fmt.Errorf("%s must be a JSON object", field)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil || object == nil {
		return fmt.Errorf("%s must be a non-null JSON object", field)
	}
	return nil
}
