package networkpolicy

import (
	"errors"
	"testing"
	"time"
)

func newTestChangeMachine(t *testing.T) (*ChangeMachine, time.Time) {
	t.Helper()
	plan, err := BuildChangePlan(validChangePlanRequest())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 9, 10, 0, 0, 0, time.UTC)
	machine, err := NewChangeMachine(plan, now)
	if err != nil {
		t.Fatal(err)
	}
	return machine, now
}

func applyEvent(t *testing.T, machine *ChangeMachine, event ChangeEvent) ChangeSnapshot {
	t.Helper()
	snapshot, err := machine.Apply(event, machine.Snapshot().Version)
	if err != nil {
		t.Fatalf("Apply(%s) error = %v", event.Type, err)
	}
	return snapshot
}

func TestChangeMachineCommitsOnlyAfterAllConnectivityProbes(t *testing.T) {
	machine, now := newTestChangeMachine(t)

	snapshot := applyEvent(t, machine, ChangeEvent{ID: "event-01", Type: EventSnapshotCaptured, SnapshotID: "recovery-01", At: now.Add(time.Second)})
	if snapshot.State != ChangeStatePreflight {
		t.Fatalf("state = %s, want preflight", snapshot.State)
	}
	if snapshot.SnapshotID != "recovery-01" {
		t.Fatalf("snapshot id = %q", snapshot.SnapshotID)
	}
	snapshot = applyEvent(t, machine, ChangeEvent{ID: "event-02", Type: EventPreflightPassed, At: now.Add(2 * time.Second)})
	if snapshot.State != ChangeStateApplyWindow {
		t.Fatalf("state = %s, want apply_window", snapshot.State)
	}
	snapshot = applyEvent(t, machine, ChangeEvent{ID: "event-03", Type: EventTemporaryApplied, At: now.Add(3 * time.Second)})
	if snapshot.State != ChangeStateProbing {
		t.Fatalf("state = %s, want probing", snapshot.State)
	}

	probeIDs := []string{"wan-link", "management-control", "lan-reachability"}
	for index, probeID := range probeIDs {
		snapshot = applyEvent(t, machine, ChangeEvent{
			ID: "probe-event-0" + string(rune('1'+index)), Type: EventProbePassed,
			ProbeID: probeID, At: now.Add(time.Duration(4+index) * time.Second),
		})
		if index < len(probeIDs)-1 && snapshot.State != ChangeStateProbing {
			t.Fatalf("state after %d probes = %s, want probing", index+1, snapshot.State)
		}
	}
	if snapshot.State != ChangeStateCommitted || !snapshot.Deadline.IsZero() {
		t.Fatalf("final snapshot = %#v", snapshot)
	}
	if len(snapshot.PassedProbeIDs) != 3 || snapshot.PassedProbeIDs[0] != "lan-reachability" {
		t.Fatalf("probe evidence is not canonical: %#v", snapshot.PassedProbeIDs)
	}
}

func TestChangeMachineProbeFailureAutomaticallyRequiresRollback(t *testing.T) {
	machine, now := newTestChangeMachine(t)
	applyEvent(t, machine, ChangeEvent{ID: "event-01", Type: EventSnapshotCaptured, SnapshotID: "recovery-01", At: now.Add(time.Second)})
	applyEvent(t, machine, ChangeEvent{ID: "event-02", Type: EventPreflightPassed, At: now.Add(2 * time.Second)})
	applyEvent(t, machine, ChangeEvent{ID: "event-03", Type: EventTemporaryApplied, At: now.Add(3 * time.Second)})

	snapshot := applyEvent(t, machine, ChangeEvent{
		ID: "event-04", Type: EventProbeFailed, ProbeID: "management-control",
		ReasonCode: "control_path_lost", At: now.Add(4 * time.Second),
	})
	if snapshot.State != ChangeStateRollbackPending || snapshot.ReasonCode != "control_path_lost" {
		t.Fatalf("snapshot = %#v", snapshot)
	}

	snapshot = applyEvent(t, machine, ChangeEvent{ID: "event-05", Type: EventRollbackSucceeded, At: now.Add(5 * time.Second)})
	if snapshot.State != ChangeStateRolledBack || snapshot.ReasonCode != "control_path_lost" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestChangeMachineTimeoutsFailClosed(t *testing.T) {
	t.Run("before temporary apply", func(t *testing.T) {
		machine, now := newTestChangeMachine(t)
		deadline := machine.Snapshot().Deadline
		snapshot := applyEvent(t, machine, ChangeEvent{ID: "event-01", Type: EventPhaseTimedOut, At: deadline})
		if snapshot.State != ChangeStateFailedClosed || snapshot.ReasonCode != "snapshot_not_available" {
			t.Fatalf("snapshot = %#v", snapshot)
		}
		_ = now
	})

	t.Run("after temporary apply", func(t *testing.T) {
		machine, now := newTestChangeMachine(t)
		applyEvent(t, machine, ChangeEvent{ID: "event-01", Type: EventSnapshotCaptured, SnapshotID: "recovery-01", At: now.Add(time.Second)})
		applyEvent(t, machine, ChangeEvent{ID: "event-02", Type: EventPreflightPassed, At: now.Add(2 * time.Second)})
		applyEvent(t, machine, ChangeEvent{ID: "event-03", Type: EventTemporaryApplied, At: now.Add(3 * time.Second)})
		deadline := machine.Snapshot().Deadline
		snapshot := applyEvent(t, machine, ChangeEvent{ID: "event-04", Type: EventPhaseTimedOut, At: deadline})
		if snapshot.State != ChangeStateRollbackPending {
			t.Fatalf("snapshot = %#v", snapshot)
		}

		rollbackDeadline := snapshot.Deadline
		snapshot = applyEvent(t, machine, ChangeEvent{ID: "event-05", Type: EventPhaseTimedOut, At: rollbackDeadline})
		if snapshot.State != ChangeStateFailedClosed || snapshot.ReasonCode != "rollback_not_confirmed" {
			t.Fatalf("snapshot = %#v", snapshot)
		}
	})
}

func TestChangeMachineRejectsLateOrForgedEvents(t *testing.T) {
	machine, now := newTestChangeMachine(t)
	if _, err := machine.Apply(ChangeEvent{ID: "early-timeout", Type: EventPhaseTimedOut, At: now.Add(time.Second)}, 1); !errors.Is(err, ErrInvalidChangeTransition) {
		t.Fatalf("early timeout error = %v", err)
	}
	if _, err := machine.Apply(ChangeEvent{ID: "late-success", Type: EventSnapshotCaptured, SnapshotID: "recovery-01", At: machine.Snapshot().Deadline.Add(time.Second)}, 1); !errors.Is(err, ErrInvalidChangeTransition) {
		t.Fatalf("late event error = %v", err)
	}
	if _, err := machine.Apply(ChangeEvent{ID: "secret-reason", Type: EventSnapshotFailed, ReasonCode: "password=unsafe", At: now.Add(time.Second)}, 1); !errors.Is(err, ErrInvalidChangePlan) {
		t.Fatalf("reason code error = %v", err)
	}
	if _, err := machine.Apply(ChangeEvent{ID: "probe-without-id", Type: EventProbeFailed, At: now.Add(time.Second)}, 1); err == nil {
		t.Fatal("probe event without probe id was accepted")
	}
}

func TestChangeMachineEventIdempotencyAndVersioning(t *testing.T) {
	machine, now := newTestChangeMachine(t)
	event := ChangeEvent{ID: "event-01", Type: EventSnapshotCaptured, SnapshotID: "recovery-01", At: now.Add(time.Second)}
	first, err := machine.Apply(event, 1)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := machine.Apply(event, 1)
	if err != nil {
		t.Fatalf("idempotent retry error = %v", err)
	}
	if retry.Version != first.Version || retry.State != first.State {
		t.Fatalf("retry mutated state: first=%#v retry=%#v", first, retry)
	}

	conflicting := event
	conflicting.SnapshotID = "recovery-02"
	if _, err := machine.Apply(conflicting, first.Version); !errors.Is(err, ErrEventIdempotencyConflict) {
		t.Fatalf("idempotency conflict error = %v", err)
	}
	if _, err := machine.Apply(ChangeEvent{ID: "event-02", Type: EventPreflightPassed, At: now.Add(2 * time.Second)}, 1); !errors.Is(err, ErrChangeVersionConflict) {
		t.Fatalf("version conflict error = %v", err)
	}
}

func TestNewChangeMachineRejectsTamperedPlan(t *testing.T) {
	plan, err := BuildChangePlan(validChangePlanRequest())
	if err != nil {
		t.Fatal(err)
	}
	plan.ForwardingDefaultDeny = false
	if _, err := NewChangeMachine(plan, time.Now()); !errors.Is(err, ErrInvalidChangePlan) {
		t.Fatalf("NewChangeMachine() error = %v", err)
	}
}
