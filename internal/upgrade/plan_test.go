package upgrade

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"control-center/internal/corecontracts"
)

func TestBuildPlanCreatesCanaryDependencyAndRingWaves(t *testing.T) {
	request := validRequest()
	original := cloneTargets(request.Targets)
	plan, err := BuildPlan(request)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if plan.ContractVersion != PlanContractV1 || plan.PlanID == "" || len(plan.Waves) != 4 {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	if !plan.Waves[0].Canary || !reflect.DeepEqual(plan.Waves[0].NodeIDs, []string{"worker-a"}) {
		t.Fatalf("unexpected canary: %#v", plan.Waves[0])
	}
	if !reflect.DeepEqual(plan.Waves[1].NodeIDs, []string{"worker-b"}) ||
		!reflect.DeepEqual(plan.Waves[2].NodeIDs, []string{"controller-a"}) ||
		!reflect.DeepEqual(plan.Waves[3].NodeIDs, []string{"controller-b"}) {
		t.Fatalf("unexpected rollout waves: %#v", plan.Waves)
	}
	if !plan.CanaryFailureStopsRollout || !plan.RollbackRequired ||
		!plan.HealthCheckBeforeNextWave || !plan.RequiresApprovedChange ||
		!plan.RequiresDurableJobs || !plan.RequiresAudit || !plan.PlanOnly ||
		plan.HostMutation {
		t.Fatalf("unsafe plan flags: %#v", plan)
	}
	if !reflect.DeepEqual(request.Targets, original) {
		t.Fatal("planner mutated caller targets")
	}
}

func TestBuildPlanIsDeterministicForTargetAndDependencyOrder(t *testing.T) {
	first := validRequest()
	firstPlan, err := BuildPlan(first)
	if err != nil {
		t.Fatal(err)
	}
	second := validRequest()
	second.Targets[2].Dependencies = []string{"worker-b", "worker-a"}
	for left, right := 0, len(second.Targets)-1; left < right; left, right = left+1, right-1 {
		second.Targets[left], second.Targets[right] = second.Targets[right], second.Targets[left]
	}
	secondPlan, err := BuildPlan(second)
	if err != nil {
		t.Fatal(err)
	}
	if secondPlan.PlanID != firstPlan.PlanID || !reflect.DeepEqual(secondPlan.Waves, firstPlan.Waves) {
		t.Fatalf("non-deterministic plans: %#v != %#v", secondPlan, firstPlan)
	}
}

func TestBuildPlanFailsClosedOnUnsafeGraphsAndQuorum(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Request)
	}{
		{name: "canary dependency", mutate: func(r *Request) { r.Targets[0].Dependencies = []string{"worker-b"} }},
		{name: "unknown dependency", mutate: func(r *Request) { r.Targets[1].Dependencies = []string{"missing"} }},
		{name: "later ring dependency", mutate: func(r *Request) { r.Targets[0].Dependencies = []string{"controller-a"} }},
		{name: "cycle", mutate: func(r *Request) {
			r.CanaryNodeID = "worker-b"
			r.Targets[0].Dependencies = []string{"controller-a"}
			r.Targets[2].Dependencies = []string{"worker-a"}
		}},
		{name: "quorum", mutate: func(r *Request) { r.MinimumHealthyControllers = 3 }},
		{name: "bad window", mutate: func(r *Request) { r.Window.EndsAt = r.Window.StartsAt.Add(time.Minute) }},
		{name: "duplicate target", mutate: func(r *Request) { r.Targets = append(r.Targets, r.Targets[0]) }},
		{name: "same version", mutate: func(r *Request) { r.Targets[0].CurrentVersion = r.Targets[0].TargetVersion }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validRequest()
			test.mutate(&request)
			if _, err := BuildPlan(request); !errors.Is(err, ErrInvalidPlan) {
				t.Fatalf("error = %v, want ErrInvalidPlan", err)
			}
		})
	}
}

func validRequest() Request {
	start := time.Date(2026, 9, 10, 1, 0, 0, 0, time.UTC)
	return Request{
		Targets: []Target{
			{NodeID: "worker-a", Role: corecontracts.RoleWorkerNode, CurrentVersion: "0.4.0", TargetVersion: "0.5.0", Ring: 0},
			{NodeID: "worker-b", Role: corecontracts.RoleWorkerNode, CurrentVersion: "0.4.0", TargetVersion: "0.5.0", Ring: 1, Dependencies: []string{"worker-a"}},
			{NodeID: "controller-a", Role: corecontracts.RoleControllerClusterMember, CurrentVersion: "0.4.0", TargetVersion: "0.5.0", Ring: 2, Dependencies: []string{"worker-a", "worker-b"}},
			{NodeID: "controller-b", Role: corecontracts.RoleControllerClusterMember, CurrentVersion: "0.4.0", TargetVersion: "0.5.0", Ring: 2, Dependencies: []string{"controller-a"}},
		},
		CanaryNodeID: "worker-a", MaxParallel: 2,
		ControllerPopulation: 3, MinimumHealthyControllers: 2,
		Window: MaintenanceWindow{StartsAt: start, EndsAt: start.Add(2 * time.Hour)},
	}
}

func cloneTargets(values []Target) []Target {
	result := make([]Target, len(values))
	for index, value := range values {
		result[index] = value
		result[index].Dependencies = append([]string(nil), value.Dependencies...)
	}
	return result
}
