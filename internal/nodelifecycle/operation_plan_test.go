package nodelifecycle

import (
	"errors"
	"reflect"
	"testing"
)

func TestBuildDrainOperationPlanIsSafeAndDeterministic(t *testing.T) {
	current := lifecycleInState(StateReady)
	request := OperationPlanRequest{
		Kind: OperationDrain,
		Placements: []WorkloadPlacement{
			{WorkloadID: "web-2", Kind: WorkloadStateless},
			{WorkloadID: "database-1", Kind: WorkloadStateful, MigrationAdapter: "postgres-replica-switchover", HealthyReplicasOutside: 1},
			{WorkloadID: "web-1", Kind: WorkloadStateless},
		},
	}
	original := append([]WorkloadPlacement(nil), request.Placements...)
	plan, err := BuildOperationPlan(current, request)
	if err != nil {
		t.Fatalf("BuildOperationPlan() error = %v", err)
	}
	if plan.ContractVersion != OperationPlanContractV1 || plan.PlanID == "" || len(plan.Steps) != 4 {
		t.Fatalf("unexpected drain plan: %#v", plan)
	}
	if !plan.PlanOnly || !plan.RequiresApprovedChange || !plan.RequiresDurableJob || !plan.RequiresAudit {
		t.Fatalf("missing safety gates: %#v", plan)
	}
	if plan.LifecycleMutation || plan.PlacementMutation || plan.HostMutation {
		t.Fatalf("planner has effects: %#v", plan)
	}
	if !reflect.DeepEqual(request.Placements, original) {
		t.Fatal("planner mutated caller placements")
	}

	reordered := request
	reordered.Placements = []WorkloadPlacement{request.Placements[2], request.Placements[0], request.Placements[1]}
	again, err := BuildOperationPlan(current, reordered)
	if err != nil {
		t.Fatal(err)
	}
	if again.PlanID != plan.PlanID {
		t.Fatalf("plan id changed with placement order: %q != %q", again.PlanID, plan.PlanID)
	}
}

func TestBuildReplaceOperationPlanRequiresCompletedDrainAndSafeStatefulMove(t *testing.T) {
	current := lifecycleInState(StateMaintenance)
	request := OperationPlanRequest{
		Kind: OperationReplace, ReplacementNodeID: "node-2",
		Placements: []WorkloadPlacement{{
			WorkloadID: "database-1", Kind: WorkloadStateful,
			MigrationAdapter: "postgres-replica-switchover", HealthyReplicasOutside: 1,
		}},
	}
	plan, err := BuildOperationPlan(current, request)
	if err != nil {
		t.Fatalf("BuildOperationPlan() error = %v", err)
	}
	if plan.ReplacementNodeID != "node-2" || len(plan.Steps) != 6 {
		t.Fatalf("unexpected replace plan: %#v", plan)
	}
	if got := plan.Steps[len(plan.Steps)-1].RequiredEvidence; !reflect.DeepEqual(got, []EvidenceCheck{
		CheckReplacementNodeReady, CheckStateSynchronized, CheckSwitchoverVerified, CheckReplacementHealthVerified,
	}) {
		t.Fatalf("retirement evidence = %#v", got)
	}
}

func TestBuildOperationPlanFailsClosed(t *testing.T) {
	ready := lifecycleInState(StateReady)
	maintenance := lifecycleInState(StateMaintenance)
	tests := []struct {
		name    string
		current NodeLifecycle
		request OperationPlanRequest
	}{
		{name: "replace before drain", current: ready, request: OperationPlanRequest{Kind: OperationReplace, ReplacementNodeID: "node-2"}},
		{name: "drain from maintenance", current: maintenance, request: OperationPlanRequest{Kind: OperationDrain}},
		{name: "same replacement node", current: maintenance, request: OperationPlanRequest{Kind: OperationReplace, ReplacementNodeID: maintenance.ObjectID}},
		{name: "stateful without adapter", current: ready, request: OperationPlanRequest{Kind: OperationDrain, Placements: []WorkloadPlacement{{WorkloadID: "db", Kind: WorkloadStateful, HealthyReplicasOutside: 1}}}},
		{name: "stateful without replica", current: ready, request: OperationPlanRequest{Kind: OperationDrain, Placements: []WorkloadPlacement{{WorkloadID: "db", Kind: WorkloadStateful, MigrationAdapter: "postgres"}}}},
		{name: "duplicate workload", current: ready, request: OperationPlanRequest{Kind: OperationDrain, Placements: []WorkloadPlacement{{WorkloadID: "web", Kind: WorkloadStateless}, {WorkloadID: "web", Kind: WorkloadStateless}}}},
		{name: "unsupported operation", current: ready, request: OperationPlanRequest{Kind: "remove"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := BuildOperationPlan(test.current, test.request); !errors.Is(err, ErrInvalidOperationPlan) {
				t.Fatalf("error = %v, want ErrInvalidOperationPlan", err)
			}
		})
	}
}
