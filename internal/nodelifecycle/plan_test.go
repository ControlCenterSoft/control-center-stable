package nodelifecycle

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"control-center/internal/corecontracts"
)

func TestBuildTransitionPlanIsExplicitlyNonMutating(t *testing.T) {
	current := lifecycleInState(StateReady)
	before := current
	rule, _ := transitionRuleFor(StateReady, StateDraining)
	next, request := validTransition(current, StateDraining, rule)
	_ = next
	evaluatedAt := current.UpdatedAt.Add(time.Minute)

	plan, err := BuildTransitionPlan(current, request, evaluatedAt)
	if err != nil {
		t.Fatalf("BuildTransitionPlan() error = %v", err)
	}
	if !plan.Accepted || !plan.PlanOnly || plan.HostMutation || plan.StateMutation || !plan.RequiresAuditedChangeJob {
		t.Fatalf("unsafe plan flags: %#v", plan)
	}
	if plan.NodeID != current.ObjectID || plan.From != StateReady || plan.To != StateDraining || plan.Type != TransitionDesired {
		t.Fatalf("unexpected transition plan: %#v", plan)
	}
	if plan.CurrentGeneration != current.Generation || plan.PlannedGeneration != current.Generation+1 {
		t.Fatalf("generation plan = %d -> %d", plan.CurrentGeneration, plan.PlannedGeneration)
	}
	if plan.BasedOnResourceVersion != current.ResourceVersion || plan.PlanID == "" {
		t.Fatalf("missing concurrency identity: %#v", plan)
	}
	if !reflect.DeepEqual(current, before) {
		t.Fatalf("current lifecycle was mutated: before=%#v after=%#v", before, current)
	}
}

func TestBuildTransitionPlanObservationPreservesGeneration(t *testing.T) {
	current := lifecycleInState(StateReady)
	rule, _ := transitionRuleFor(StateReady, StateOffline)
	_, request := validTransition(current, StateOffline, rule)
	plan, err := BuildTransitionPlan(current, request, current.UpdatedAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("BuildTransitionPlan() error = %v", err)
	}
	if plan.PlannedGeneration != current.Generation || plan.Type != TransitionObservation {
		t.Fatalf("observation changed generation: %#v", plan)
	}
}

func TestBuildTransitionPlanIDIsStableForEvidenceSet(t *testing.T) {
	current := lifecycleInState(StateDraining)
	rule, _ := transitionRuleFor(StateDraining, StateMaintenance)
	_, firstRequest := validTransition(current, StateMaintenance, rule)
	secondRequest := firstRequest
	secondRequest.Evidence.PassedChecks = append([]EvidenceCheck(nil), firstRequest.Evidence.PassedChecks...)
	for left, right := 0, len(secondRequest.Evidence.PassedChecks)-1; left < right; left, right = left+1, right-1 {
		secondRequest.Evidence.PassedChecks[left], secondRequest.Evidence.PassedChecks[right] = secondRequest.Evidence.PassedChecks[right], secondRequest.Evidence.PassedChecks[left]
	}

	first, err := BuildTransitionPlan(current, firstRequest, current.UpdatedAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildTransitionPlan(current, secondRequest, current.UpdatedAt.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if first.PlanID != second.PlanID {
		t.Fatalf("plan ids differ for one evidence set: %q != %q", first.PlanID, second.PlanID)
	}
	if !reflect.DeepEqual(firstRequest.Evidence.PassedChecks, rule.requiredChecks) {
		t.Fatal("fingerprint sorting mutated the request evidence")
	}
	first.RequiredChecks[0] = CheckReadinessPassed
	third, err := BuildTransitionPlan(current, firstRequest, current.UpdatedAt.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(third.RequiredChecks, rule.requiredChecks) {
		t.Fatal("returned plan mutated the transition rule")
	}
}

func TestBuildTransitionPlanPropagatesContractFailures(t *testing.T) {
	current := lifecycleInState(StateReady)
	rule, _ := transitionRuleFor(StateReady, StateDraining)
	_, valid := validTransition(current, StateDraining, rule)
	tests := []struct {
		name   string
		mutate func(*TransitionRequest)
		want   error
	}{
		{name: "stale version", mutate: func(r *TransitionRequest) { r.Precondition.ResourceVersion = "rv:stale" }, want: corecontracts.ErrPreconditionFailed},
		{name: "invalid edge", mutate: func(r *TransitionRequest) { r.To = StateRetired; r.Reason = "unsafe shortcut" }, want: corecontracts.ErrInvalidTransition},
		{name: "missing evidence", mutate: func(r *TransitionRequest) { r.Evidence.PassedChecks = nil }, want: ErrInvalidEvidence},
		{name: "unknown type", mutate: func(r *TransitionRequest) { r.Type = "automatic" }, want: corecontracts.ErrInvalidTransition},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			request.Evidence.PassedChecks = append([]EvidenceCheck(nil), valid.Evidence.PassedChecks...)
			test.mutate(&request)
			if _, err := BuildTransitionPlan(current, request, current.UpdatedAt.Add(time.Minute)); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestBuildTransitionPlanRejectsInvalidClockAndGenerationOverflow(t *testing.T) {
	current := lifecycleInState(StateReady)
	rule, _ := transitionRuleFor(StateReady, StateDraining)
	_, request := validTransition(current, StateDraining, rule)
	for _, evaluatedAt := range []time.Time{{}, current.UpdatedAt, current.UpdatedAt.Add(-time.Second)} {
		if _, err := BuildTransitionPlan(current, request, evaluatedAt); !errors.Is(err, ErrInvalidPlan) {
			t.Fatalf("evaluated_at=%v error=%v, want ErrInvalidPlan", evaluatedAt, err)
		}
	}
	current.Generation = math.MaxUint64
	request.Precondition.Generation = uint64Pointer(current.Generation)
	request.Precondition.ResourceVersion = current.ResourceVersion
	if _, err := BuildTransitionPlan(current, request, current.UpdatedAt.Add(time.Minute)); !errors.Is(err, corecontracts.ErrInvalidTransition) {
		t.Fatalf("generation overflow error = %v, want ErrInvalidTransition", err)
	}
}
