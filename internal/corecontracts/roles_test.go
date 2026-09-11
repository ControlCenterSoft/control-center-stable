package corecontracts

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestAllCoreRolesHaveValidAssignments(t *testing.T) {
	topology := testTopology(t)
	roles := []NodeRole{
		RoleManagementNode,
		RoleGlobalController,
		RoleSiteController,
		RoleControllerClusterMember,
		RoleWorkerNode,
		RoleManagedNode,
		RoleAgent,
		RoleDataNode,
		RoleConsensusNode,
		RoleRepositoryNode,
		RoleTelemetryNode,
		RoleBackupRepositoryNode,
		RoleEdgeGateway,
	}
	assignments := make([]RoleAssignment, 0, len(roles))
	for index, role := range roles {
		assignment := testRoleAssignment(fmt.Sprintf("assignment-%02d", index), role)
		if err := ValidateRoleAssignment(assignment, topology); err != nil {
			t.Fatalf("ValidateRoleAssignment(%q) error = %v", role, err)
		}
		assignments = append(assignments, assignment)
	}
	if err := ValidateRoleAssignments(assignments, topology); err != nil {
		t.Fatalf("ValidateRoleAssignments() error = %v", err)
	}
}

func TestValidateRoleAssignmentRejectsInvalidBindings(t *testing.T) {
	topology := testTopology(t)
	valid := testRoleAssignment("assignment-worker", RoleWorkerNode)
	tests := []struct {
		name   string
		mutate func(*RoleAssignment)
	}{
		{name: "unknown role", mutate: func(a *RoleAssignment) { a.Role = "regional-controller" }},
		{name: "coordinator is elected state", mutate: func(a *RoleAssignment) { a.Role = "cluster-coordinator" }},
		{name: "missing physical node", mutate: func(a *RoleAssignment) { a.TargetNodeID = "" }},
		{name: "missing service identity", mutate: func(a *RoleAssignment) { a.ServiceIdentityID = "" }},
		{name: "unknown scope", mutate: func(a *RoleAssignment) { a.ScopeID = "missing" }},
		{name: "owner below target", mutate: func(a *RoleAssignment) {
			a.ScopeID = "site-a"
			a.OwnerScope = "site-a-resources"
		}},
		{name: "owner lacks delegation", mutate: func(a *RoleAssignment) { a.OwnerScope = "region-a" }},
		{name: "unknown site", mutate: func(a *RoleAssignment) { a.SiteID = "missing" }},
		{name: "unknown zone", mutate: func(a *RoleAssignment) { a.ManagementZoneID = "missing" }},
		{name: "global controller on resource", mutate: func(a *RoleAssignment) { a.Role = RoleGlobalController }},
		{name: "site controller without site", mutate: func(a *RoleAssignment) {
			a.Role = RoleSiteController
			a.SiteID = ""
		}},
		{name: "cluster member outside controller scope", mutate: func(a *RoleAssignment) { a.Role = RoleControllerClusterMember }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assignment := valid
			test.mutate(&assignment)
			if err := ValidateRoleAssignment(assignment, topology); !errors.Is(err, ErrInvalidRoleAssignment) {
				t.Fatalf("ValidateRoleAssignment() error = %v, want ErrInvalidRoleAssignment", err)
			}
		})
	}
}

func TestValidateRoleAssignmentsEnforcesOneLogicalControllerPerScope(t *testing.T) {
	topology := testTopology(t)
	first := testRoleAssignment("controller-1", RoleGlobalController)
	second := testRoleAssignment("controller-2", RoleGlobalController)
	second.TargetNodeID = "node-2"
	if err := ValidateRoleAssignments([]RoleAssignment{first, second}, topology); err != nil {
		t.Fatalf("cluster members sharing service identity rejected: %v", err)
	}

	second.ServiceIdentityID = "independent-control-plane"
	if err := ValidateRoleAssignments([]RoleAssignment{first, second}, topology); !errors.Is(err, ErrInvalidRoleAssignment) {
		t.Fatalf("independent controllers error = %v, want ErrInvalidRoleAssignment", err)
	}

	member := testRoleAssignment("member-1", RoleControllerClusterMember)
	member.ServiceIdentityID = "independent-control-plane"
	if err := ValidateRoleAssignments([]RoleAssignment{first, member}, topology); !errors.Is(err, ErrInvalidRoleAssignment) {
		t.Fatalf("independent cluster member error = %v, want ErrInvalidRoleAssignment", err)
	}

	siteController := testRoleAssignment("site-controller-1", RoleSiteController)
	siteController.ServiceIdentityID = first.ServiceIdentityID
	if err := ValidateRoleAssignments([]RoleAssignment{first, siteController}, topology); !errors.Is(err, ErrInvalidRoleAssignment) {
		t.Fatalf("cross-scope controller identity error = %v, want ErrInvalidRoleAssignment", err)
	}
}

func TestValidateRoleAssignmentsRejectsDuplicateIdentityAndBinding(t *testing.T) {
	topology := testTopology(t)
	first := testRoleAssignment("worker-1", RoleWorkerNode)
	duplicateObject := first
	if err := ValidateRoleAssignments([]RoleAssignment{first, duplicateObject}, topology); !errors.Is(err, ErrInvalidRoleAssignment) {
		t.Fatalf("duplicate object error = %v, want ErrInvalidRoleAssignment", err)
	}

	duplicateBinding := first
	duplicateBinding.ObjectID = "worker-2"
	duplicateBinding.ResourceVersion = "rv:worker-2"
	if err := ValidateRoleAssignments([]RoleAssignment{first, duplicateBinding}, topology); !errors.Is(err, ErrInvalidRoleAssignment) {
		t.Fatalf("duplicate binding error = %v, want ErrInvalidRoleAssignment", err)
	}
}

func TestValidateRoleAssignmentSuccessorPreservesLogicalIdentity(t *testing.T) {
	topology := testTopology(t)
	current := testRoleAssignment("worker-1", RoleWorkerNode)
	next := current
	next.TargetNodeID = "node-2"
	next.Generation++
	next.ResourceVersion = "rv:worker-1-moved"
	next.UpdatedAt = current.UpdatedAt.Add(1)
	if err := ValidateRoleAssignmentSuccessor(current, next, topology); err != nil {
		t.Fatalf("valid re-placement error = %v", err)
	}

	missingGeneration := next
	missingGeneration.Generation = current.Generation
	if err := ValidateRoleAssignmentSuccessor(current, missingGeneration, topology); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("missing generation error = %v, want ErrInvalidTransition", err)
	}

	changedIdentity := next
	changedIdentity.ServiceIdentityID = "service:replacement"
	if err := ValidateRoleAssignmentSuccessor(current, changedIdentity, topology); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("changed identity error = %v, want ErrInvalidTransition", err)
	}
}

func TestRoleAssignmentJSONFlattensDistributedMetadata(t *testing.T) {
	assignment := testRoleAssignment("worker-1", RoleWorkerNode)
	encoded, err := json.Marshal(assignment)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatalf("Unmarshal(map) error = %v", err)
	}
	for _, field := range []string{"object_id", "scope_id", "owner_scope", "generation", "resource_version", "target_node_id", "service_identity_id", "role"} {
		if _, exists := object[field]; !exists {
			t.Fatalf("JSON lacks required top-level field %q: %s", field, encoded)
		}
	}
	if _, nested := object["ObjectMetadata"]; nested {
		t.Fatalf("metadata was unexpectedly nested: %s", encoded)
	}

	var decoded RoleAssignment
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal(RoleAssignment) error = %v", err)
	}
	if err := ValidateRoleAssignment(decoded, testTopology(t)); err != nil {
		t.Fatalf("JSON round-trip assignment invalid: %v", err)
	}
}

func testRoleAssignment(objectID string, role NodeRole) RoleAssignment {
	scopeID := "site-a-resources"
	ownerScope := "global"
	siteID := "site-a"
	zoneID := "zone-a"
	if role == RoleGlobalController || role == RoleControllerClusterMember {
		scopeID = "global"
		siteID = ""
		zoneID = ""
	}
	if role == RoleSiteController {
		scopeID = "site-a"
	}
	metadata := testMetadata(objectID, scopeID, ownerScope, 1, "rv:"+objectID)
	serviceIdentityID := "service:" + string(role)
	if role == RoleControllerClusterMember {
		serviceIdentityID = "service:" + string(RoleGlobalController)
	}
	return RoleAssignment{
		ObjectMetadata:    metadata,
		TargetNodeID:      "node-1",
		ServiceIdentityID: serviceIdentityID,
		Role:              role,
		SiteID:            siteID,
		ManagementZoneID:  zoneID,
	}
}
