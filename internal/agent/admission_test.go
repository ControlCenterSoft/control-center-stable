package agent

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"control-center/internal/corecontracts"
)

func TestBuildEnrollmentAdmissionPlanBindsBootstrapEnrollmentAndRoles(t *testing.T) {
	request, topology := validAdmissionRequest(t)
	original := append([]corecontracts.RoleAssignment(nil), request.Assignments...)

	plan, err := BuildEnrollmentAdmissionPlan(request, topology)
	if err != nil {
		t.Fatalf("BuildEnrollmentAdmissionPlan() error = %v", err)
	}
	if plan.ContractVersion != EnrollmentAdmissionContractV1 || plan.PlanID == "" {
		t.Fatalf("missing plan identity: %#v", plan)
	}
	if plan.NodeID != request.Grant.NodeID || plan.ScopeID != request.Grant.ScopeID {
		t.Fatalf("wrong binding: %#v", plan)
	}
	if !plan.RequiresApprovedChange || !plan.RequiresDurableJob || !plan.RequiresAudit {
		t.Fatalf("orchestration gates are not explicit: %#v", plan)
	}
	if plan.PersistsEnrollment || plan.MutatesRoles || plan.HostMutation || plan.NetworkMutation {
		t.Fatalf("planner unexpectedly has effects: %#v", plan)
	}
	if !reflect.DeepEqual(request.Assignments, original) {
		t.Fatal("planner mutated caller assignments")
	}

	reordered := request
	reordered.Assignments = append([]corecontracts.RoleAssignment(nil), request.Assignments...)
	for left, right := 0, len(reordered.Assignments)-1; left < right; left, right = left+1, right-1 {
		reordered.Assignments[left], reordered.Assignments[right] = reordered.Assignments[right], reordered.Assignments[left]
	}
	again, err := BuildEnrollmentAdmissionPlan(reordered, topology)
	if err != nil {
		t.Fatal(err)
	}
	if again.PlanID != plan.PlanID || !reflect.DeepEqual(again.AssignmentIDs, plan.AssignmentIDs) {
		t.Fatalf("plan is not deterministic: %#v != %#v", again, plan)
	}
}

func TestBuildEnrollmentAdmissionPlanFailsClosedOnCrossContractMismatch(t *testing.T) {
	valid, topology := validAdmissionRequest(t)
	tests := []struct {
		name   string
		mutate func(*EnrollmentAdmissionRequest)
	}{
		{name: "wrong node", mutate: func(r *EnrollmentAdmissionRequest) { r.Grant.NodeID = "node-other" }},
		{name: "wrong scope", mutate: func(r *EnrollmentAdmissionRequest) { r.Assignments[0].ScopeID = "global" }},
		{name: "wrong site", mutate: func(r *EnrollmentAdmissionRequest) { r.Assignments[0].SiteID = "" }},
		{name: "future evidence", mutate: func(r *EnrollmentAdmissionRequest) {
			future := r.Grant.ConsumedAt.Add(time.Minute)
			r.Enrollment.CollectedAt = &future
		}},
		{name: "missing role", mutate: func(r *EnrollmentAdmissionRequest) { r.Assignments = r.Assignments[:2] }},
		{name: "extra role", mutate: func(r *EnrollmentAdmissionRequest) {
			extra := r.Assignments[0]
			extra.ObjectID = "role-data"
			extra.ResourceVersion = "rv:role-data"
			extra.Role = corecontracts.RoleDataNode
			extra.ServiceIdentityID = "service:data-node"
			r.Assignments = append(r.Assignments, extra)
		}},
		{name: "replayed shape without consumed time", mutate: func(r *EnrollmentAdmissionRequest) { r.Grant.ConsumedAt = time.Time{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			request.Assignments = append([]corecontracts.RoleAssignment(nil), valid.Assignments...)
			test.mutate(&request)
			if _, err := BuildEnrollmentAdmissionPlan(request, topology); !errors.Is(err, ErrEnrollmentAdmissionRejected) {
				t.Fatalf("error = %v, want ErrEnrollmentAdmissionRejected", err)
			}
		})
	}
}

func validAdmissionRequest(t *testing.T) (EnrollmentAdmissionRequest, corecontracts.Topology) {
	t.Helper()
	topology, err := corecontracts.NewTopology(
		[]corecontracts.Scope{
			{ID: "global", Kind: corecontracts.ScopeGlobal, Name: "Global"},
			{ID: "site-a", Kind: corecontracts.ScopeSite, Name: "Site A", ParentID: "global", DelegatedAuthorities: []corecontracts.DelegatedAuthority{corecontracts.DelegateDesiredState}},
			{ID: "site-a-resources", Kind: corecontracts.ScopeResource, Name: "Site A resources", ParentID: "site-a", DelegatedAuthorities: []corecontracts.DelegatedAuthority{corecontracts.DelegateDesiredState}},
		},
		[]corecontracts.Site{{ID: "site-a", Name: "Site A", ScopeID: "site-a"}},
		[]corecontracts.ManagementZone{{ID: "zone-primary", Name: "Primary", ScopeID: "site-a-resources", SiteID: "site-a"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	enrollment := validV2Enrollment()
	consumedAt := enrollment.CollectedAt.Add(time.Minute)
	normalized, err := NormalizeEnrollment(enrollment)
	if err != nil {
		t.Fatal(err)
	}
	roles := append([]corecontracts.NodeRole(nil), normalized.Roles...)
	assignments := make([]corecontracts.RoleAssignment, 0, len(roles))
	for _, role := range roles {
		assignments = append(assignments, corecontracts.RoleAssignment{
			ObjectMetadata: corecontracts.ObjectMetadata{
				ObjectID: "role-" + string(role), ScopeID: "site-a-resources", OwnerScope: "global",
				Generation: 1, ResourceVersion: "rv:" + string(role),
				CreatedAt: consumedAt, UpdatedAt: consumedAt,
			},
			TargetNodeID: "node-001", ServiceIdentityID: "service:" + string(role),
			Role: role, SiteID: "site-a", ManagementZoneID: "zone-primary",
		})
	}
	return EnrollmentAdmissionRequest{
		Grant: BootstrapGrant{
			TokenID: "bootstrap-001", NodeID: "node-001", ScopeID: "site-a-resources",
			Transport: BootstrapTransportSSH, ConsumedAt: consumedAt,
		},
		Enrollment: enrollment, Assignments: assignments,
	}, topology
}
