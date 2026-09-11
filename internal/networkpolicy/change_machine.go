package networkpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"
)

var (
	ErrInvalidChangeTransition  = errors.New("invalid network change transition")
	ErrChangeVersionConflict    = errors.New("network change version conflict")
	ErrEventIdempotencyConflict = errors.New("network change event idempotency conflict")
)

type ChangeState string

const (
	ChangeStateSnapshotPending ChangeState = "snapshot_pending"
	ChangeStatePreflight       ChangeState = "preflight"
	ChangeStateApplyWindow     ChangeState = "apply_window"
	ChangeStateProbing         ChangeState = "probing"
	ChangeStateRollbackPending ChangeState = "rollback_pending"
	ChangeStateCommitted       ChangeState = "committed"
	ChangeStateRolledBack      ChangeState = "rolled_back"
	ChangeStateFailedClosed    ChangeState = "failed_closed"
)

func (state ChangeState) Terminal() bool {
	return state == ChangeStateCommitted || state == ChangeStateRolledBack || state == ChangeStateFailedClosed
}

type ChangeEventType string

const (
	EventSnapshotCaptured  ChangeEventType = "snapshot_captured"
	EventSnapshotFailed    ChangeEventType = "snapshot_failed"
	EventPreflightPassed   ChangeEventType = "preflight_passed"
	EventPreflightFailed   ChangeEventType = "preflight_failed"
	EventTemporaryApplied  ChangeEventType = "temporary_applied"
	EventProbePassed       ChangeEventType = "probe_passed"
	EventProbeFailed       ChangeEventType = "probe_failed"
	EventPhaseTimedOut     ChangeEventType = "phase_timed_out"
	EventRollbackSucceeded ChangeEventType = "rollback_succeeded"
	EventRollbackFailed    ChangeEventType = "rollback_failed"
)

// ChangeEvent is a closed input to the safety state machine. ReasonCode is a
// bounded identifier, never an execution payload or secret.
type ChangeEvent struct {
	ID         string          `json:"id"`
	Type       ChangeEventType `json:"type"`
	SnapshotID string          `json:"snapshot_id,omitempty"`
	ProbeID    string          `json:"probe_id,omitempty"`
	ReasonCode string          `json:"reason_code,omitempty"`
	At         time.Time       `json:"at"`
}

type ChangeSnapshot struct {
	PlanID         string      `json:"plan_id"`
	SnapshotID     string      `json:"snapshot_id,omitempty"`
	State          ChangeState `json:"state"`
	Version        uint64      `json:"version"`
	Deadline       time.Time   `json:"deadline"`
	PassedProbeIDs []string    `json:"passed_probe_ids"`
	ReasonCode     string      `json:"reason_code,omitempty"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

type ChangeMachine struct {
	mu              sync.RWMutex
	plan            ChangePlan
	snapshot        ChangeSnapshot
	passed          map[string]struct{}
	processedEvents map[string]string
}

func NewChangeMachine(plan ChangePlan, now time.Time) (*ChangeMachine, error) {
	if now.IsZero() {
		return nil, errors.New("network change start time is required")
	}
	validated, err := BuildChangePlan(ChangePlanRequest{
		NodeID: plan.NodeID, RevisionID: plan.RevisionID, Interfaces: plan.Interfaces,
		Forwarding: plan.Forwarding, Probes: plan.Probes, Timeouts: plan.Timeouts,
	})
	if err != nil || !reflect.DeepEqual(validated, plan) {
		return nil, fmt.Errorf("%w: plan integrity check failed", ErrInvalidChangePlan)
	}
	now = now.UTC()
	return &ChangeMachine{
		plan: plan,
		snapshot: ChangeSnapshot{
			PlanID: plan.PlanID, State: ChangeStateSnapshotPending, Version: 1,
			Deadline: now.Add(plan.Timeouts.Snapshot), UpdatedAt: now,
		},
		passed:          make(map[string]struct{}, len(plan.Probes)),
		processedEvents: make(map[string]string),
	}, nil
}

func (machine *ChangeMachine) Snapshot() ChangeSnapshot {
	machine.mu.RLock()
	defer machine.mu.RUnlock()
	return machine.copySnapshot()
}

// Apply consumes an idempotent event with optimistic version checking. Once a
// temporary apply may have occurred, any timeout or failed probe enters
// rollback_pending. Before apply, failures stop closed without rollback.
func (machine *ChangeMachine) Apply(event ChangeEvent, expectedVersion uint64) (ChangeSnapshot, error) {
	machine.mu.Lock()
	defer machine.mu.Unlock()

	digest, err := validateAndDigestEvent(event)
	if err != nil {
		return machine.copySnapshot(), err
	}
	if previous, exists := machine.processedEvents[event.ID]; exists {
		if previous != digest {
			return machine.copySnapshot(), ErrEventIdempotencyConflict
		}
		return machine.copySnapshot(), nil
	}
	if machine.snapshot.Version != expectedVersion {
		return machine.copySnapshot(), fmt.Errorf("%w: expected %d, current %d", ErrChangeVersionConflict, expectedVersion, machine.snapshot.Version)
	}
	if machine.snapshot.State.Terminal() {
		return machine.copySnapshot(), fmt.Errorf("%w: terminal state %s", ErrInvalidChangeTransition, machine.snapshot.State)
	}

	event.At = event.At.UTC()
	if event.At.Before(machine.snapshot.UpdatedAt) {
		return machine.copySnapshot(), fmt.Errorf("%w: event time precedes current state", ErrInvalidChangeTransition)
	}
	if event.Type == EventPhaseTimedOut && event.At.Before(machine.snapshot.Deadline) {
		return machine.copySnapshot(), fmt.Errorf("%w: phase deadline has not elapsed", ErrInvalidChangeTransition)
	}
	if event.Type != EventPhaseTimedOut && event.At.After(machine.snapshot.Deadline) {
		return machine.copySnapshot(), fmt.Errorf("%w: phase deadline exceeded; phase_timed_out event required", ErrInvalidChangeTransition)
	}
	if err := machine.transition(event); err != nil {
		return machine.copySnapshot(), err
	}
	machine.processedEvents[event.ID] = digest
	machine.snapshot.Version++
	machine.snapshot.UpdatedAt = event.At
	return machine.copySnapshot(), nil
}

func (machine *ChangeMachine) transition(event ChangeEvent) error {
	switch machine.snapshot.State {
	case ChangeStateSnapshotPending:
		switch event.Type {
		case EventSnapshotCaptured:
			machine.snapshot.SnapshotID = event.SnapshotID
			machine.enter(ChangeStatePreflight, event.At, machine.plan.Timeouts.Preflight, "")
		case EventSnapshotFailed, EventPhaseTimedOut:
			machine.enter(ChangeStateFailedClosed, event.At, 0, eventDetail(event, "snapshot_not_available"))
		default:
			return invalidEvent(machine.snapshot.State, event.Type)
		}
	case ChangeStatePreflight:
		switch event.Type {
		case EventPreflightPassed:
			machine.enter(ChangeStateApplyWindow, event.At, machine.plan.Timeouts.ApplyWindow, "")
		case EventPreflightFailed, EventPhaseTimedOut:
			machine.enter(ChangeStateFailedClosed, event.At, 0, eventDetail(event, "preflight_not_safe"))
		default:
			return invalidEvent(machine.snapshot.State, event.Type)
		}
	case ChangeStateApplyWindow:
		switch event.Type {
		case EventTemporaryApplied:
			deadline := event.At.Add(machine.plan.Timeouts.Probe)
			applyDeadline := machine.snapshot.Deadline
			if deadline.After(applyDeadline) {
				deadline = applyDeadline
			}
			machine.snapshot.State = ChangeStateProbing
			machine.snapshot.Deadline = deadline
			machine.snapshot.ReasonCode = ""
		case EventPhaseTimedOut:
			machine.enter(ChangeStateRollbackPending, event.At, machine.plan.Timeouts.Rollback, eventDetail(event, "apply_window_timeout"))
		default:
			return invalidEvent(machine.snapshot.State, event.Type)
		}
	case ChangeStateProbing:
		switch event.Type {
		case EventProbePassed:
			if !machine.hasProbe(event.ProbeID) {
				return fmt.Errorf("%w: unknown probe %q", ErrInvalidChangeTransition, event.ProbeID)
			}
			if _, duplicate := machine.passed[event.ProbeID]; duplicate {
				return fmt.Errorf("%w: probe %q already passed", ErrInvalidChangeTransition, event.ProbeID)
			}
			machine.passed[event.ProbeID] = struct{}{}
			if len(machine.passed) == len(machine.plan.Probes) {
				machine.enter(ChangeStateCommitted, event.At, 0, "")
			}
		case EventProbeFailed, EventPhaseTimedOut:
			if event.Type == EventProbeFailed && !machine.hasProbe(event.ProbeID) {
				return fmt.Errorf("%w: unknown probe %q", ErrInvalidChangeTransition, event.ProbeID)
			}
			machine.enter(ChangeStateRollbackPending, event.At, machine.plan.Timeouts.Rollback, eventDetail(event, "connectivity_validation_failed"))
		default:
			return invalidEvent(machine.snapshot.State, event.Type)
		}
	case ChangeStateRollbackPending:
		switch event.Type {
		case EventRollbackSucceeded:
			machine.enter(ChangeStateRolledBack, event.At, 0, machine.snapshot.ReasonCode)
		case EventRollbackFailed, EventPhaseTimedOut:
			machine.enter(ChangeStateFailedClosed, event.At, 0, eventDetail(event, "rollback_not_confirmed"))
		default:
			return invalidEvent(machine.snapshot.State, event.Type)
		}
	default:
		return fmt.Errorf("%w: state %s", ErrInvalidChangeTransition, machine.snapshot.State)
	}
	return nil
}

func (machine *ChangeMachine) enter(state ChangeState, at time.Time, timeout time.Duration, reason string) {
	machine.snapshot.State = state
	machine.snapshot.ReasonCode = reason
	if timeout > 0 {
		machine.snapshot.Deadline = at.Add(timeout)
	} else {
		machine.snapshot.Deadline = time.Time{}
	}
}

func (machine *ChangeMachine) hasProbe(id string) bool {
	for _, probe := range machine.plan.Probes {
		if probe.ID == id {
			return true
		}
	}
	return false
}

func (machine *ChangeMachine) copySnapshot() ChangeSnapshot {
	snapshot := machine.snapshot
	snapshot.PassedProbeIDs = make([]string, 0, len(machine.passed))
	for _, probe := range machine.plan.Probes {
		if _, passed := machine.passed[probe.ID]; passed {
			snapshot.PassedProbeIDs = append(snapshot.PassedProbeIDs, probe.ID)
		}
	}
	return snapshot
}

func validateAndDigestEvent(event ChangeEvent) (string, error) {
	if _, err := normalizeIdentifier("event.id", event.ID); err != nil {
		return "", err
	}
	if event.At.IsZero() {
		return "", errors.New("network change event time is required")
	}
	if event.ReasonCode != "" {
		if _, err := normalizeIdentifier("event.reason_code", event.ReasonCode); err != nil {
			return "", err
		}
	}
	if event.ProbeID != "" {
		if _, err := normalizeIdentifier("event.probe_id", event.ProbeID); err != nil {
			return "", err
		}
	}
	if (event.Type == EventProbePassed || event.Type == EventProbeFailed) != (event.ProbeID != "") {
		return "", errors.New("probe events require probe_id and other events forbid it")
	}
	if event.Type == EventSnapshotCaptured {
		if _, err := normalizeIdentifier("event.snapshot_id", event.SnapshotID); err != nil {
			return "", err
		}
	} else if event.SnapshotID != "" {
		return "", errors.New("only snapshot_captured events may contain snapshot_id")
	}
	isFailure := event.Type == EventSnapshotFailed || event.Type == EventPreflightFailed ||
		event.Type == EventProbeFailed || event.Type == EventPhaseTimedOut || event.Type == EventRollbackFailed
	if event.ReasonCode != "" && !isFailure {
		return "", errors.New("successful network change events must not contain reason_code")
	}
	switch event.Type {
	case EventSnapshotCaptured, EventSnapshotFailed, EventPreflightPassed, EventPreflightFailed,
		EventTemporaryApplied, EventProbePassed, EventProbeFailed, EventPhaseTimedOut,
		EventRollbackSucceeded, EventRollbackFailed:
	default:
		return "", fmt.Errorf("unknown network change event %q", event.Type)
	}
	event.At = event.At.UTC()
	document, err := json.Marshal(event)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(document)
	return hex.EncodeToString(digest[:]), nil
}

func eventDetail(event ChangeEvent, fallback string) string {
	if event.ReasonCode != "" {
		return event.ReasonCode
	}
	return fallback
}

func invalidEvent(state ChangeState, event ChangeEventType) error {
	return fmt.Errorf("%w: event %s from %s", ErrInvalidChangeTransition, event, state)
}
