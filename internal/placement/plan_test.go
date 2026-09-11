package placement

import (
	"errors"
	"testing"

	"control-center/internal/corecontracts"
)

func request() Request {
	return Request{WorkloadID: "svc-01", Class: WorkloadStateless, RequiredRole: corecontracts.RoleWorkerNode, AllowedSites: []string{"site-a"}, Resources: Resources{500, 512, 10}, TopologyGeneration: 7, TopologyResourceVersion: "rv:topology:7"}
}
func candidate(id string, used uint64) Candidate {
	return Candidate{NodeID: id, SiteID: "site-a", ZoneID: "zone-a", Roles: []corecontracts.NodeRole{corecontracts.RoleWorkerNode}, Healthy: true, Schedulable: true, Allocatable: Resources{4000, 8192, 100}, Used: Resources{used, 1024, 20}}
}

func TestBuildPlanIsDeterministicAndNonAuthoritative(t *testing.T) {
	input := []Candidate{candidate("node-b", 1500), candidate("node-a", 500)}
	first, err := BuildPlan(request(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildPlan(request(), []Candidate{input[1], input[0]})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("plan depends on input order: %#v %#v", first, second)
	}
	if first.SelectedNodeID != "node-a" || !first.PlanOnly || first.HostMutation || !first.RequiresApprovedChange || !first.RequiresDurableJob || !first.RequiresAudit || !first.RevalidateBeforeExecute {
		t.Fatalf("unsafe plan: %#v", first)
	}
}

func TestBuildPlanEnforcesTopologyRoleHealthAffinityAndCapacity(t *testing.T) {
	r := request()
	r.AllowedZones = []string{"zone-a"}
	r.AntiAffinityNodeIDs = []string{"node-a"}
	unhealthy := candidate("node-b", 0)
	unhealthy.Healthy = false
	wrongRole := candidate("node-c", 0)
	wrongRole.Roles = []corecontracts.NodeRole{corecontracts.RoleDataNode}
	full := candidate("node-d", 0)
	full.Used.MemoryMiB = 8000
	_, err := BuildPlan(r, []Candidate{candidate("node-a", 0), unhealthy, wrongRole, full})
	if !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("expected fail-closed placement, got %v", err)
	}
}

func TestStatefulMoveRequiresAdapterAndReplica(t *testing.T) {
	r := request()
	r.Class = WorkloadStateful
	r.CurrentNodeID = "node-old"
	r.AntiAffinityNodeIDs = []string{"node-old"}
	_, err := BuildPlan(r, []Candidate{candidate("node-new", 0)})
	if !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("expected safety rejection, got %v", err)
	}
	r.MigrationAdapter = "provider.replica-switchover.v1"
	r.HealthyReplicasOutsideNode = 1
	plan, err := BuildPlan(r, []Candidate{candidate("node-new", 0)})
	if err != nil {
		t.Fatal(err)
	}
	if plan.SelectedNodeID != "node-new" {
		t.Fatal(plan.SelectedNodeID)
	}
}

func TestTieBreaksByCanonicalNodeID(t *testing.T) {
	plan, err := BuildPlan(request(), []Candidate{candidate("node-z", 100), candidate("node-a", 100)})
	if err != nil {
		t.Fatal(err)
	}
	if plan.SelectedNodeID != "node-a" {
		t.Fatal(plan.SelectedNodeID)
	}
}
