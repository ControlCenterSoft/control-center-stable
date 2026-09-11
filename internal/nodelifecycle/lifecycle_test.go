package nodelifecycle

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"control-center/internal/corecontracts"
)

var contractStates = []State{
	StateDiscovered,
	StateEnrolling,
	StateReady,
	StateDegraded,
	StateDraining,
	StateMaintenance,
	StateUpdating,
	StateReplacing,
	StateRemoving,
	StateOffline,
	StateRecovering,
	StateRetired,
}

func TestStateContractIsClosedAndRetiredIsTerminal(t *testing.T) {
	for _, state := range contractStates {
		if !state.Valid() {
			t.Fatalf("state %q is not valid", state)
		}
		if got := state.Terminal(); got != (state == StateRetired) {
			t.Fatalf("State(%q).Terminal() = %t", state, got)
		}
	}
	for _, state := range []State{"", "active", "failed", "retiring", "READY"} {
		if state.Valid() {
			t.Fatalf("unknown state %q accepted", state)
		}
	}
	if len(transitionRules[StateRetired]) != 0 {
		t.Fatalf("retired has successors: %#v", transitionRules[StateRetired])
	}
}

func TestValidateInitialRequiresCanonicalDiscoveryEnvelope(t *testing.T) {
	valid := initialLifecycle()
	if err := ValidateInitial(valid); err != nil {
		t.Fatalf("ValidateInitial() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*NodeLifecycle)
	}{
		{name: "not discovered", mutate: func(l *NodeLifecycle) {
			l.State = StateEnrolling
		}},
		{name: "generation is not one", mutate: func(l *NodeLifecycle) {
			l.Generation = 2
		}},
		{name: "updated time differs", mutate: func(l *NodeLifecycle) {
			l.UpdatedAt = l.UpdatedAt.Add(time.Second)
		}},
		{name: "state time differs", mutate: func(l *NodeLifecycle) {
			l.StateChangedAt = l.StateChangedAt.Add(time.Second)
			l.UpdatedAt = l.StateChangedAt
		}},
		{name: "invalid metadata", mutate: func(l *NodeLifecycle) {
			l.ResourceVersion = ""
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if err := ValidateInitial(candidate); !errors.Is(err, ErrInvalidLifecycle) {
				t.Fatalf("ValidateInitial() error = %v, want ErrInvalidLifecycle", err)
			}
		})
	}
}

func TestValidateRejectsMalformedLifecycleObjects(t *testing.T) {
	valid := lifecycleInState(StateReady)
	tests := []struct {
		name   string
		mutate func(*NodeLifecycle)
	}{
		{name: "unknown state", mutate: func(l *NodeLifecycle) { l.State = "active" }},
		{name: "missing state time", mutate: func(l *NodeLifecycle) { l.StateChangedAt = time.Time{} }},
		{name: "state before creation", mutate: func(l *NodeLifecycle) { l.StateChangedAt = l.CreatedAt.Add(-time.Second) }},
		{name: "state after update", mutate: func(l *NodeLifecycle) { l.StateChangedAt = l.UpdatedAt.Add(time.Second) }},
		{name: "unexpected healthy reason", mutate: func(l *NodeLifecycle) { l.Reason = "old incident" }},
		{name: "invalid object id", mutate: func(l *NodeLifecycle) { l.ObjectID = "node/1" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if err := Validate(candidate); !errors.Is(err, ErrInvalidLifecycle) {
				t.Fatalf("Validate() error = %v, want ErrInvalidLifecycle", err)
			}
		})
	}
}

func TestIncidentAndRetirementStatesRequireSafeReasons(t *testing.T) {
	for _, state := range []State{StateDegraded, StateOffline, StateRecovering, StateRetired} {
		t.Run(string(state), func(t *testing.T) {
			candidate := lifecycleInState(state)
			if err := Validate(candidate); err != nil {
				t.Fatalf("valid reason rejected: %v", err)
			}
			candidate.Reason = ""
			if err := Validate(candidate); !errors.Is(err, ErrInvalidLifecycle) {
				t.Fatalf("missing reason error = %v, want ErrInvalidLifecycle", err)
			}
		})
	}

	tests := []string{" surrounding whitespace ", "line\nbreak", strings.Repeat("r", maxReasonLength+1), string([]byte{0xff})}
	for _, reason := range tests {
		candidate := lifecycleInState(StateOffline)
		candidate.Reason = reason
		if err := Validate(candidate); !errors.Is(err, ErrInvalidLifecycle) {
			t.Fatalf("unsafe reason %q error = %v, want ErrInvalidLifecycle", reason, err)
		}
	}

	multibyte := lifecycleInState(StateOffline)
	multibyte.Reason = strings.Repeat("я", maxReasonLength)
	if err := Validate(multibyte); err != nil {
		t.Fatalf("valid multibyte reason rejected: %v", err)
	}
}

func TestEveryDeclaredTransitionAcceptsCompleteEvidence(t *testing.T) {
	for from, targets := range transitionRules {
		for to, rule := range targets {
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				current := lifecycleInState(from)
				next, request := validTransition(current, to, rule)
				if err := ValidateTransition(current, next, request); err != nil {
					t.Fatalf("ValidateTransition() error = %v", err)
				}
			})
		}
	}
}

func TestEveryUndeclaredTransitionFailsClosed(t *testing.T) {
	allChecks := allEvidenceChecks()
	for _, from := range contractStates {
		for _, to := range contractStates {
			if _, declared := transitionRuleFor(from, to); declared {
				continue
			}
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				current := lifecycleInState(from)
				next := successor(current, to, TransitionObservation)
				request := transitionRequest(current, next, TransitionObservation, allChecks)
				if err := ValidateTransition(current, next, request); !errors.Is(err, corecontracts.ErrInvalidTransition) {
					t.Fatalf("ValidateTransition() error = %v, want canonical ErrInvalidTransition", err)
				}
			})
		}
	}
}

func TestTransitionPreconditionIsMandatoryAndExact(t *testing.T) {
	current := lifecycleInState(StateReady)
	rule, _ := transitionRuleFor(StateReady, StateDraining)
	next, valid := validTransition(current, StateDraining, rule)
	generation := current.Generation
	tests := []struct {
		name         string
		precondition corecontracts.ObjectPrecondition
		want         error
	}{
		{name: "missing", precondition: corecontracts.ObjectPrecondition{}, want: corecontracts.ErrPreconditionRequired},
		{name: "missing version", precondition: corecontracts.ObjectPrecondition{ObjectID: current.ObjectID}, want: corecontracts.ErrPreconditionRequired},
		{name: "wrong object", precondition: corecontracts.ObjectPrecondition{ObjectID: "node-2", ResourceVersion: current.ResourceVersion}, want: corecontracts.ErrPreconditionFailed},
		{name: "stale version", precondition: corecontracts.ObjectPrecondition{ObjectID: current.ObjectID, ResourceVersion: "rv:stale"}, want: corecontracts.ErrPreconditionFailed},
		{name: "stale generation", precondition: corecontracts.ObjectPrecondition{ObjectID: current.ObjectID, ResourceVersion: current.ResourceVersion, Generation: uint64Pointer(generation - 1)}, want: corecontracts.ErrPreconditionFailed},
		{name: "malformed version", precondition: corecontracts.ObjectPrecondition{ObjectID: current.ObjectID, ResourceVersion: "bad version"}, want: corecontracts.ErrInvalidPrecondition},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			request.Precondition = test.precondition
			if err := ValidateTransition(current, next, request); !errors.Is(err, test.want) {
				t.Fatalf("ValidateTransition() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestTransitionGenerationAndResourceVersionSemantics(t *testing.T) {
	t.Run("desired transition increments generation", func(t *testing.T) {
		current := lifecycleInState(StateReady)
		rule, _ := transitionRuleFor(StateReady, StateDraining)
		next, request := validTransition(current, StateDraining, rule)
		next.Generation = current.Generation
		if err := ValidateTransition(current, next, request); !errors.Is(err, corecontracts.ErrInvalidTransition) {
			t.Fatalf("error = %v, want ErrInvalidTransition", err)
		}
	})

	t.Run("observation transition preserves generation", func(t *testing.T) {
		current := lifecycleInState(StateEnrolling)
		rule, _ := transitionRuleFor(StateEnrolling, StateReady)
		next, request := validTransition(current, StateReady, rule)
		next.Generation++
		if err := ValidateTransition(current, next, request); !errors.Is(err, corecontracts.ErrInvalidTransition) {
			t.Fatalf("error = %v, want ErrInvalidTransition", err)
		}
	})

	t.Run("every stored update gets a new opaque version", func(t *testing.T) {
		current := lifecycleInState(StateReady)
		rule, _ := transitionRuleFor(StateReady, StateOffline)
		next, request := validTransition(current, StateOffline, rule)
		next.ResourceVersion = current.ResourceVersion
		if err := ValidateTransition(current, next, request); !errors.Is(err, corecontracts.ErrInvalidTransition) {
			t.Fatalf("error = %v, want ErrInvalidTransition", err)
		}
	})
}

func TestTransitionEvidenceIsTypedCompleteAndUnique(t *testing.T) {
	current := lifecycleInState(StateDraining)
	rule, _ := transitionRuleFor(StateDraining, StateMaintenance)
	next, valid := validTransition(current, StateMaintenance, rule)

	for index, missing := range rule.requiredChecks {
		t.Run("missing_"+string(missing), func(t *testing.T) {
			request := valid
			request.Evidence.PassedChecks = append([]EvidenceCheck(nil), rule.requiredChecks[:index]...)
			request.Evidence.PassedChecks = append(request.Evidence.PassedChecks, rule.requiredChecks[index+1:]...)
			if err := ValidateTransition(current, next, request); !errors.Is(err, ErrInvalidEvidence) {
				t.Fatalf("error = %v, want ErrInvalidEvidence", err)
			}
		})
	}

	t.Run("unknown check", func(t *testing.T) {
		request := valid
		request.Evidence.PassedChecks = append(request.Evidence.PassedChecks, "filesystem-copied")
		if err := ValidateTransition(current, next, request); !errors.Is(err, ErrInvalidEvidence) {
			t.Fatalf("error = %v, want ErrInvalidEvidence", err)
		}
	})

	t.Run("duplicate check", func(t *testing.T) {
		request := valid
		request.Evidence.PassedChecks = append(request.Evidence.PassedChecks, request.Evidence.PassedChecks[0])
		if err := ValidateTransition(current, next, request); !errors.Is(err, ErrInvalidEvidence) {
			t.Fatalf("error = %v, want ErrInvalidEvidence", err)
		}
	})

	t.Run("extra known evidence", func(t *testing.T) {
		request := valid
		request.Evidence.PassedChecks = append(request.Evidence.PassedChecks, CheckReadinessPassed)
		if err := ValidateTransition(current, next, request); err != nil {
			t.Fatalf("extra typed evidence rejected: %v", err)
		}
	})
}

func TestMaintenanceReplaceAndRemoveSafetyGates(t *testing.T) {
	tests := []struct {
		from State
		to   State
	}{
		{from: StateReady, to: StateDraining},
		{from: StateDraining, to: StateMaintenance},
		{from: StateMaintenance, to: StateReplacing},
		{from: StateReplacing, to: StateRetired},
		{from: StateMaintenance, to: StateRemoving},
		{from: StateRemoving, to: StateRetired},
		{from: StateOffline, to: StateRetired},
	}
	for _, test := range tests {
		t.Run(string(test.from)+"_to_"+string(test.to), func(t *testing.T) {
			rule, _ := transitionRuleFor(test.from, test.to)
			if len(rule.requiredChecks) == 0 {
				t.Fatal("safety-sensitive transition has no evidence gate")
			}
			current := lifecycleInState(test.from)
			next, request := validTransition(current, test.to, rule)
			request.Evidence.PassedChecks = request.Evidence.PassedChecks[:len(request.Evidence.PassedChecks)-1]
			if err := ValidateTransition(current, next, request); !errors.Is(err, ErrInvalidEvidence) {
				t.Fatalf("incomplete safety evidence error = %v, want ErrInvalidEvidence", err)
			}
		})
	}
}

func TestTransitionRequestAndSuccessorMustAgree(t *testing.T) {
	current := lifecycleInState(StateReady)
	rule, _ := transitionRuleFor(StateReady, StateOffline)
	next, valid := validTransition(current, StateOffline, rule)
	tests := []struct {
		name   string
		mutate func(*NodeLifecycle, *TransitionRequest)
	}{
		{name: "different target", mutate: func(_ *NodeLifecycle, r *TransitionRequest) { r.To = StateDegraded }},
		{name: "different reason", mutate: func(_ *NodeLifecycle, r *TransitionRequest) { r.Reason = "different incident" }},
		{name: "wrong transition type", mutate: func(_ *NodeLifecycle, r *TransitionRequest) { r.Type = TransitionDesired }},
		{name: "unknown transition type", mutate: func(_ *NodeLifecycle, r *TransitionRequest) { r.Type = "automatic" }},
		{name: "scope changed", mutate: func(n *NodeLifecycle, _ *TransitionRequest) { n.ScopeID = "site-b" }},
		{name: "state time not update time", mutate: func(n *NodeLifecycle, _ *TransitionRequest) { n.StateChangedAt = n.StateChangedAt.Add(-time.Second) }},
		{name: "state time did not advance", mutate: func(n *NodeLifecycle, _ *TransitionRequest) {
			n.StateChangedAt = current.StateChangedAt
			n.UpdatedAt = current.StateChangedAt
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := next
			request := valid
			test.mutate(&candidate, &request)
			if err := ValidateTransition(current, candidate, request); !errors.Is(err, corecontracts.ErrInvalidTransition) {
				t.Fatalf("error = %v, want ErrInvalidTransition", err)
			}
		})
	}
}

func TestNodeLifecycleJSONFlattensCanonicalMetadata(t *testing.T) {
	lifecycle := lifecycleInState(StateOffline)
	encoded, err := json.Marshal(lifecycle)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatalf("Unmarshal(map) error = %v", err)
	}
	for _, field := range []string{"object_id", "scope_id", "owner_scope", "generation", "resource_version", "state", "state_changed_at", "reason"} {
		if _, exists := object[field]; !exists {
			t.Fatalf("JSON lacks field %q: %s", field, encoded)
		}
	}
	if _, nested := object["ObjectMetadata"]; nested {
		t.Fatalf("metadata was unexpectedly nested: %s", encoded)
	}

	var decoded NodeLifecycle
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal(NodeLifecycle) error = %v", err)
	}
	if err := Validate(decoded); err != nil {
		t.Fatalf("JSON round-trip lifecycle invalid: %v", err)
	}
}

func initialLifecycle() NodeLifecycle {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	return NodeLifecycle{
		ObjectMetadata: corecontracts.ObjectMetadata{
			ObjectID:        "node-1",
			ScopeID:         "site-a-resources",
			OwnerScope:      "site-a",
			Generation:      1,
			ResourceVersion: "rv:node-1:1",
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		State:          StateDiscovered,
		StateChangedAt: now,
	}
}

func lifecycleInState(state State) NodeLifecycle {
	lifecycle := initialLifecycle()
	lifecycle.Generation = 7
	lifecycle.ResourceVersion = "rv:node-1:7"
	lifecycle.UpdatedAt = lifecycle.CreatedAt.Add(7 * time.Minute)
	lifecycle.StateChangedAt = lifecycle.UpdatedAt
	lifecycle.State = state
	lifecycle.Reason = reasonFor(state)
	return lifecycle
}

func validTransition(current NodeLifecycle, to State, rule transitionRule) (NodeLifecycle, TransitionRequest) {
	next := successor(current, to, rule.typeOf)
	return next, transitionRequest(current, next, rule.typeOf, rule.requiredChecks)
}

func successor(current NodeLifecycle, to State, typeOf TransitionType) NodeLifecycle {
	next := current
	next.State = to
	next.Reason = reasonFor(to)
	next.UpdatedAt = current.UpdatedAt.Add(time.Minute)
	next.StateChangedAt = next.UpdatedAt
	next.ResourceVersion = "rv:next:" + string(to)
	if typeOf == TransitionDesired {
		next.Generation++
	}
	return next
}

func transitionRequest(current, next NodeLifecycle, typeOf TransitionType, evidence []EvidenceCheck) TransitionRequest {
	generation := current.Generation
	return TransitionRequest{
		To:     next.State,
		Type:   typeOf,
		Reason: next.Reason,
		Precondition: corecontracts.ObjectPrecondition{
			ObjectID:        current.ObjectID,
			ResourceVersion: current.ResourceVersion,
			Generation:      &generation,
		},
		Evidence: TransitionEvidence{PassedChecks: append([]EvidenceCheck(nil), evidence...)},
	}
}

func reasonFor(state State) string {
	switch state {
	case StateDegraded:
		return "readiness checks degraded"
	case StateOffline:
		return "heartbeat lease expired"
	case StateRecovering:
		return "verified recovery is in progress"
	case StateRetired:
		return "decommission completed"
	default:
		return ""
	}
}

func allEvidenceChecks() []EvidenceCheck {
	return []EvidenceCheck{
		CheckEnrollmentAuthorized,
		CheckIdentityVerified,
		CheckReadinessPassed,
		CheckMaintenancePreflightPassed,
		CheckSchedulingDisabled,
		CheckSchedulingEnabled,
		CheckDrainComplete,
		CheckNoActiveJobs,
		CheckNoActivePlacements,
		CheckStatefulWorkloadsSafe,
		CheckOperationApproved,
		CheckOperationCancelled,
		CheckRollbackVerified,
		CheckUpdateVerified,
		CheckReplacementNodeReady,
		CheckStateSynchronized,
		CheckSwitchoverVerified,
		CheckReplacementHealthVerified,
		CheckRemovalApproved,
		CheckRemovalVerified,
		CheckRecoveryStarted,
		CheckRecoveryVerified,
		CheckRetirementApproved,
		CheckNoManagedState,
	}
}

func uint64Pointer(value uint64) *uint64 { return &value }
