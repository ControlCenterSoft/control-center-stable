package corecontracts

import (
	"errors"
	"fmt"
)

var ErrInvalidRoleAssignment = errors.New("invalid node role assignment")

// NodeRole is a closed set of core roles. Cluster coordinator is deliberately
// absent: it is elected runtime state, not a manually assignable role.
type NodeRole string

const (
	RoleManagementNode          NodeRole = "management-node"
	RoleGlobalController        NodeRole = "global-controller"
	RoleSiteController          NodeRole = "site-controller"
	RoleControllerClusterMember NodeRole = "controller-cluster-member"
	RoleWorkerNode              NodeRole = "worker-node"
	RoleManagedNode             NodeRole = "managed-node"
	RoleAgent                   NodeRole = "agent"
	RoleDataNode                NodeRole = "data-node"
	RoleConsensusNode           NodeRole = "consensus-node"
	RoleRepositoryNode          NodeRole = "repository-node"
	RoleTelemetryNode           NodeRole = "telemetry-node"
	RoleBackupRepositoryNode    NodeRole = "backup-repository-node"
	RoleEdgeGateway             NodeRole = "edge-gateway"
)

// Valid reports whether a role belongs to the core contract.
func (r NodeRole) Valid() bool {
	switch r {
	case RoleManagementNode,
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
		RoleEdgeGateway:
		return true
	default:
		return false
	}
}

// RoleAssignment binds a logical role/service identity to a physical node.
// Re-placement changes TargetNodeID while ObjectID and ServiceIdentityID remain
// stable and generation advances. The contract does not execute that change.
type RoleAssignment struct {
	ObjectMetadata
	TargetNodeID      string   `json:"target_node_id"`
	ServiceIdentityID string   `json:"service_identity_id"`
	Role              NodeRole `json:"role"`
	SiteID            string   `json:"site_id,omitempty"`
	ManagementZoneID  string   `json:"management_zone_id,omitempty"`
}

// ValidateRoleAssignment validates one assignment against a complete topology.
func ValidateRoleAssignment(assignment RoleAssignment, topology Topology) error {
	if err := assignment.ObjectMetadata.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRoleAssignment, err)
	}
	if err := validateIdentifier("target_node_id", assignment.TargetNodeID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRoleAssignment, err)
	}
	if err := validateIdentifier("service_identity_id", assignment.ServiceIdentityID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRoleAssignment, err)
	}
	if !assignment.Role.Valid() {
		return fmt.Errorf("%w: unsupported role %q", ErrInvalidRoleAssignment, assignment.Role)
	}
	scope, exists := topology.Scope(assignment.ScopeID)
	if !exists {
		return fmt.Errorf("%w: unknown assignment scope %q", ErrInvalidRoleAssignment, assignment.ScopeID)
	}
	if !topology.Contains(assignment.OwnerScope, assignment.ScopeID) {
		return fmt.Errorf("%w: owner scope %q is not an ancestor of assignment scope %q", ErrInvalidRoleAssignment, assignment.OwnerScope, assignment.ScopeID)
	}
	if assignment.OwnerScope != topology.RootID() && !topology.IsDelegated(assignment.OwnerScope, DelegateDesiredState) {
		return fmt.Errorf("%w: owner scope %q has no desired-state delegation", ErrInvalidRoleAssignment, assignment.OwnerScope)
	}

	var site Site
	if assignment.SiteID != "" {
		var siteExists bool
		site, siteExists = topology.Site(assignment.SiteID)
		if !siteExists {
			return fmt.Errorf("%w: unknown site %q", ErrInvalidRoleAssignment, assignment.SiteID)
		}
		if !topology.Contains(site.ScopeID, assignment.ScopeID) {
			return fmt.Errorf("%w: assignment scope %q is outside site %q", ErrInvalidRoleAssignment, assignment.ScopeID, assignment.SiteID)
		}
	}

	switch assignment.Role {
	case RoleGlobalController:
		if scope.Kind != ScopeGlobal {
			return fmt.Errorf("%w: global controller must be assigned to a global scope", ErrInvalidRoleAssignment)
		}
		if assignment.SiteID != "" {
			return fmt.Errorf("%w: global controller cannot be restricted to a site", ErrInvalidRoleAssignment)
		}
	case RoleSiteController:
		if assignment.SiteID == "" {
			return fmt.Errorf("%w: site controller requires site_id", ErrInvalidRoleAssignment)
		}
		if scope.Kind != ScopeSite || site.ScopeID != assignment.ScopeID {
			return fmt.Errorf("%w: site controller must be assigned directly to its site scope", ErrInvalidRoleAssignment)
		}
	case RoleControllerClusterMember:
		if scope.Kind != ScopeGlobal && scope.Kind != ScopeSite {
			return fmt.Errorf("%w: controller cluster member requires a global or site scope", ErrInvalidRoleAssignment)
		}
		if scope.Kind == ScopeGlobal && assignment.SiteID != "" {
			return fmt.Errorf("%w: global controller cluster member cannot be restricted to a site", ErrInvalidRoleAssignment)
		}
		if scope.Kind == ScopeSite && (assignment.SiteID == "" || site.ScopeID != assignment.ScopeID) {
			return fmt.Errorf("%w: site controller cluster member must reference its site", ErrInvalidRoleAssignment)
		}
	}

	if assignment.ManagementZoneID != "" {
		zone, zoneExists := topology.ManagementZone(assignment.ManagementZoneID)
		if !zoneExists {
			return fmt.Errorf("%w: unknown management zone %q", ErrInvalidRoleAssignment, assignment.ManagementZoneID)
		}
		if !topology.Contains(zone.ScopeID, assignment.ScopeID) {
			return fmt.Errorf("%w: assignment scope %q is outside management zone %q", ErrInvalidRoleAssignment, assignment.ScopeID, assignment.ManagementZoneID)
		}
		if assignment.SiteID != "" && zone.SiteID != "" && zone.SiteID != assignment.SiteID {
			return fmt.Errorf("%w: site %q and management zone %q disagree", ErrInvalidRoleAssignment, assignment.SiteID, assignment.ManagementZoneID)
		}
	}
	return nil
}

// ValidateRoleAssignmentSuccessor verifies a role re-placement without
// applying it. The logical role and service identities are immutable; moving
// the physical target or topology binding requires exactly one generation
// increment and a new resource version.
func ValidateRoleAssignmentSuccessor(current, next RoleAssignment, topology Topology) error {
	if err := ValidateRoleAssignment(current, topology); err != nil {
		return err
	}
	if err := ValidateRoleAssignment(next, topology); err != nil {
		return err
	}
	if current.Role != next.Role {
		return fmt.Errorf("%w: role is immutable", ErrInvalidTransition)
	}
	if current.ServiceIdentityID != next.ServiceIdentityID {
		return fmt.Errorf("%w: service_identity_id is immutable", ErrInvalidTransition)
	}
	desiredChanged := current.TargetNodeID != next.TargetNodeID ||
		current.ScopeID != next.ScopeID ||
		current.OwnerScope != next.OwnerScope ||
		current.SiteID != next.SiteID ||
		current.ManagementZoneID != next.ManagementZoneID
	return ValidateSuccessor(current.ObjectMetadata, next.ObjectMetadata, desiredChanged)
}

// ValidateRoleAssignments validates a complete set. Controller assignments in
// one scope must share one logical service identity, preventing independent
// masters while still permitting multiple physical cluster members.
func ValidateRoleAssignments(assignments []RoleAssignment, topology Topology) error {
	objectIDs := make(map[string]struct{}, len(assignments))
	bindings := make(map[string]string, len(assignments))
	controlPlanes := make(map[string]string)
	controlPlaneScopes := make(map[string]string)
	for _, assignment := range assignments {
		if err := ValidateRoleAssignment(assignment, topology); err != nil {
			return err
		}
		if _, exists := objectIDs[assignment.ObjectID]; exists {
			return fmt.Errorf("%w: duplicate assignment object %q", ErrInvalidRoleAssignment, assignment.ObjectID)
		}
		objectIDs[assignment.ObjectID] = struct{}{}

		bindingKey := assignment.TargetNodeID + "\x00" + string(assignment.Role) + "\x00" + assignment.ServiceIdentityID
		if other, exists := bindings[bindingKey]; exists {
			return fmt.Errorf("%w: assignments %q and %q duplicate a node/role/service binding", ErrInvalidRoleAssignment, other, assignment.ObjectID)
		}
		bindings[bindingKey] = assignment.ObjectID

		if assignment.Role != RoleGlobalController && assignment.Role != RoleSiteController && assignment.Role != RoleControllerClusterMember {
			continue
		}
		controlPlaneKey := assignment.ScopeID
		if identity, exists := controlPlanes[controlPlaneKey]; exists && identity != assignment.ServiceIdentityID {
			return fmt.Errorf("%w: scope %q declares independent %s identities %q and %q", ErrInvalidRoleAssignment, assignment.ScopeID, assignment.Role, identity, assignment.ServiceIdentityID)
		}
		if otherScope, exists := controlPlaneScopes[assignment.ServiceIdentityID]; exists && otherScope != assignment.ScopeID {
			return fmt.Errorf("%w: controller service identity %q is shared by scopes %q and %q", ErrInvalidRoleAssignment, assignment.ServiceIdentityID, otherScope, assignment.ScopeID)
		}
		controlPlanes[controlPlaneKey] = assignment.ServiceIdentityID
		controlPlaneScopes[assignment.ServiceIdentityID] = assignment.ScopeID
	}
	return nil
}
