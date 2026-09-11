package change_test

import (
	"errors"
	"testing"
	"time"

	"control-center/internal/orchestration/change"
	"control-center/internal/orchestration/config"
	"control-center/internal/orchestration/policy"
)

func TestChangeApprovalAndStateMachine(t *testing.T) {
	now := time.Unix(1, 0)
	revision, err := config.NewRevision("rev-1", 1, now, []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	decision := policy.Decision{Effect: policy.EffectAllow, Risk: policy.RiskHigh, Reason: "approved path", PolicyID: "p1", Requirement: policy.ApprovalRequirement{Minimum: 1, Permission: "changes.approve", DistinctActors: true, ProhibitRequester: true}}
	machine, err := change.New("change-1", "service.ensure", "alice", revision, decision, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.Transition(change.StateQueued, 1, now); !errors.Is(err, change.ErrInvalidTransition) {
		t.Fatalf("expected approval gate, got %v", err)
	}
	if err := machine.Approve(policy.Approval{Actor: "bob", Permissions: []string{"changes.approve"}, ApprovedAt: now}, 1, now); err != nil {
		t.Fatal(err)
	}
	if got := machine.Snapshot().State; got != change.StateApproved {
		t.Fatalf("state = %s, want approved", got)
	}
	if err := machine.Transition(change.StateQueued, 2, now); err != nil {
		t.Fatal(err)
	}
	if err := machine.Transition(change.StateSucceeded, 3, now); !errors.Is(err, change.ErrInvalidTransition) {
		t.Fatalf("expected invalid transition, got %v", err)
	}
}
