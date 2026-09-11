package rbac

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
)

type Permission string

const (
	PermissionAll                      Permission = "*"
	PermissionOverviewRead             Permission = "system.overview.read"
	PermissionUsersRead                Permission = "identity.users.read"
	PermissionUsersWrite               Permission = "identity.users.write"
	PermissionRolesRead                Permission = "identity.roles.read"
	PermissionRolesWrite               Permission = "identity.roles.write"
	PermissionAuditRead                Permission = "audit.events.read"
	PermissionResourcesRead            Permission = "resources.read"
	PermissionRevisionsWrite           Permission = "config.revisions.write"
	PermissionActionsRead              Permission = "orchestration.actions.read"
	PermissionChangesWrite             Permission = "orchestration.changes.write"
	PermissionChangesApprove           Permission = "orchestration.changes.approve"
	PermissionJobsRead                 Permission = "orchestration.jobs.read"
	PermissionJobsCancel               Permission = "orchestration.jobs.cancel"
	PermissionActionsExecute           Permission = "orchestration.actions.execute"
	PermissionNodeEnrollmentPlan       Permission = "nodes.enrollment.plan"
	PermissionNodeLifecycleRead        Permission = "nodes.lifecycle.read"
	PermissionNodeLifecyclePlan        Permission = "nodes.lifecycle.plan"
	PermissionAutomationPlan           Permission = "automation.plan"
	PermissionPXEPlan                  Permission = "pxe.plan"
	PermissionMarketRead               Permission = "market.manifests.read"
	PermissionDomainProviderResolve    Permission = "domain.provider.resolve"
	PermissionDomainLifecyclePlan      Permission = "domain.lifecycle.plan"
	PermissionInventoryNormalize       Permission = "inventory.normalize"
	PermissionAgentEnrollmentNormalize Permission = "agent.enrollment.normalize"
	PermissionInventoryReconcile       Permission = "inventory.reconcile"
	PermissionInventoryFreshness       Permission = "inventory.freshness.evaluate"
	PermissionAgentHeartbeatEvaluate   Permission = "agent.heartbeat.evaluate"
	PermissionAgentLeaseEvaluate       Permission = "agent.lease.evaluate"
	PermissionCoreObjectsRead          Permission = "core.objects.read"
	PermissionCoreObjectsWrite         Permission = "core.objects.write"
)

type ScopeKind string

const (
	ScopeGlobal   ScopeKind = "global"
	ScopeTenant   ScopeKind = "tenant"
	ScopeSite     ScopeKind = "site"
	ScopeResource ScopeKind = "resource"
)

type Scope struct {
	Kind ScopeKind `json:"kind"`
	ID   string    `json:"id,omitempty"`
}

func GlobalScope() Scope { return Scope{Kind: ScopeGlobal} }

func (s Scope) Valid() bool {
	switch s.Kind {
	case ScopeGlobal:
		return s.ID == ""
	case ScopeTenant, ScopeSite, ScopeResource:
		return strings.TrimSpace(s.ID) != ""
	default:
		return false
	}
}

type Role struct {
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Permissions []Permission `json:"permissions"`
}

// PermissionDefinition is the canonical persisted definition of a built-in
// permission. Keeping this registry next to the permission constants lets the
// database upgrade qualification prove that every permission used by a
// built-in role is present after a clean install or upgrade.
type PermissionDefinition struct {
	Name        Permission
	Description string
}

var builtinPermissions = []PermissionDefinition{
	{Name: PermissionAll, Description: "All permissions; valid only through an explicit role binding"},
	{Name: PermissionOverviewRead, Description: "Read system overview"},
	{Name: PermissionUsersRead, Description: "Read local identities"},
	{Name: PermissionUsersWrite, Description: "Manage local identities"},
	{Name: PermissionRolesRead, Description: "Read roles and bindings"},
	{Name: PermissionRolesWrite, Description: "Manage roles and bindings"},
	{Name: PermissionAuditRead, Description: "Read security audit events"},
	{Name: PermissionResourcesRead, Description: "Read resource inventory and observed state"},
	{Name: PermissionRevisionsWrite, Description: "Create immutable configuration revisions"},
	{Name: PermissionActionsRead, Description: "Read executable action descriptors"},
	{Name: PermissionChangesWrite, Description: "Create changes"},
	{Name: PermissionChangesApprove, Description: "Approve high-risk changes"},
	{Name: PermissionJobsRead, Description: "Read job execution state and outputs"},
	{Name: PermissionJobsCancel, Description: "Request job cancellation"},
	{Name: PermissionActionsExecute, Description: "Execute allowlisted orchestration actions"},
	{Name: PermissionNodeEnrollmentPlan, Description: "Plan node enrollment"},
	{Name: PermissionNodeLifecycleRead, Description: "Read node lifecycle state"},
	{Name: PermissionNodeLifecyclePlan, Description: "Plan guarded node lifecycle transitions"},
	{Name: PermissionAutomationPlan, Description: "Plan software automation without host mutation"},
	{Name: PermissionPXEPlan, Description: "Plan PXE deployment without host mutation"},
	{Name: PermissionMarketRead, Description: "Read Market manifests"},
	{Name: PermissionDomainProviderResolve, Description: "Resolve compatible directory-service providers"},
	{Name: PermissionDomainLifecyclePlan, Description: "Plan directory-service lifecycle operations"},
	{Name: PermissionInventoryNormalize, Description: "Normalize inventory observations"},
	{Name: PermissionAgentEnrollmentNormalize, Description: "Normalize agent enrollment contracts"},
	{Name: PermissionInventoryReconcile, Description: "Reconcile inventory observations"},
	{Name: PermissionInventoryFreshness, Description: "Evaluate inventory freshness"},
	{Name: PermissionAgentHeartbeatEvaluate, Description: "Evaluate agent heartbeat health"},
	{Name: PermissionAgentLeaseEvaluate, Description: "Evaluate agent lease state"},
	{Name: PermissionCoreObjectsRead, Description: "Read distributed core topology and state objects"},
	{Name: PermissionCoreObjectsWrite, Description: "Apply typed distributed core object changes through Change and Job"},
}

// BuiltinPermissions returns a defensive copy of the canonical built-in
// permission registry. Custom permissions are deliberately outside this set.
func BuiltinPermissions() []PermissionDefinition {
	return append([]PermissionDefinition(nil), builtinPermissions...)
}

type Binding struct {
	SubjectID string `json:"subject_id"`
	RoleName  string `json:"role_name"`
	Scope     Scope  `json:"scope"`
}

type EffectiveGrant struct {
	RoleName    string       `json:"role_name"`
	Scope       Scope        `json:"scope"`
	Permissions []Permission `json:"permissions"`
}

type Checker interface {
	Allowed(subjectID string, permission Permission, target Scope) bool
}

// Introspector exposes the grants already assigned to one authenticated
// subject. It is deliberately read-only and does not imply permission to read
// bindings for another identity.
type Introspector interface {
	EffectiveGrants(context.Context, string) ([]EffectiveGrant, error)
}

type Authorizer struct {
	mu       sync.RWMutex
	roles    map[string]map[Permission]struct{}
	bindings []Binding
}

func NewAuthorizer() *Authorizer {
	return &Authorizer{roles: make(map[string]map[Permission]struct{})}
}

func (a *Authorizer) RegisterRole(role Role) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	name := strings.TrimSpace(role.Name)
	if name == "" || len(role.Permissions) == 0 {
		return errors.New("role name and permissions are required")
	}
	permissions := make(map[Permission]struct{}, len(role.Permissions))
	for _, permission := range role.Permissions {
		if strings.TrimSpace(string(permission)) == "" {
			return errors.New("empty permission is not allowed")
		}
		permissions[permission] = struct{}{}
	}
	a.roles[name] = permissions
	return nil
}

func (a *Authorizer) Bind(binding Binding) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if strings.TrimSpace(binding.SubjectID) == "" || !binding.Scope.Valid() {
		return errors.New("subject and valid scope are required")
	}
	if _, exists := a.roles[binding.RoleName]; !exists {
		return errors.New("unknown role")
	}
	a.bindings = append(a.bindings, binding)
	return nil
}

func (a *Authorizer) Allowed(subjectID string, permission Permission, target Scope) bool {
	if strings.TrimSpace(subjectID) == "" || strings.TrimSpace(string(permission)) == "" || !target.Valid() {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, binding := range a.bindings {
		if binding.SubjectID != subjectID || !scopeContains(binding.Scope, target) {
			continue
		}
		permissions, exists := a.roles[binding.RoleName]
		if !exists {
			continue
		}
		if _, all := permissions[PermissionAll]; all {
			return true
		}
		if _, exact := permissions[permission]; exact {
			return true
		}
	}
	return false
}

func (a *Authorizer) EffectiveGrants(ctx context.Context, subjectID string) ([]EffectiveGrant, error) {
	if a == nil || strings.TrimSpace(subjectID) == "" {
		return nil, errors.New("subject is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()

	grants := make([]EffectiveGrant, 0)
	for _, binding := range a.bindings {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if binding.SubjectID != subjectID {
			continue
		}
		permissions, exists := a.roles[binding.RoleName]
		if !exists {
			continue
		}
		permissionList := make([]Permission, 0, len(permissions))
		for permission := range permissions {
			permissionList = append(permissionList, permission)
		}
		sort.Slice(permissionList, func(i, j int) bool { return permissionList[i] < permissionList[j] })
		grants = append(grants, EffectiveGrant{RoleName: binding.RoleName, Scope: binding.Scope, Permissions: permissionList})
	}
	sort.Slice(grants, func(i, j int) bool {
		if grants[i].Scope.Kind != grants[j].Scope.Kind {
			return grants[i].Scope.Kind < grants[j].Scope.Kind
		}
		if grants[i].Scope.ID != grants[j].Scope.ID {
			return grants[i].Scope.ID < grants[j].Scope.ID
		}
		return grants[i].RoleName < grants[j].RoleName
	})
	return grants, nil
}

func scopeContains(binding, target Scope) bool {
	if binding.Kind == ScopeGlobal && binding.ID == "" {
		return true
	}
	return binding.Kind == target.Kind && binding.ID == target.ID
}

func BuiltinRoles() []Role {
	return []Role{
		{Name: "administrator", Description: "Full Control Center administration", Permissions: []Permission{PermissionAll}},
		{Name: "operator", Description: "Routine identity and system operations", Permissions: []Permission{
			PermissionOverviewRead, PermissionUsersRead, PermissionUsersWrite, PermissionRolesRead,
			PermissionResourcesRead,
			PermissionRevisionsWrite, PermissionActionsRead, PermissionChangesWrite,
			PermissionJobsRead, PermissionJobsCancel,
			PermissionNodeEnrollmentPlan, PermissionNodeLifecycleRead, PermissionNodeLifecyclePlan,
			PermissionAutomationPlan, PermissionPXEPlan, PermissionMarketRead,
			PermissionDomainProviderResolve, PermissionDomainLifecyclePlan, PermissionInventoryNormalize, PermissionAgentEnrollmentNormalize,
			PermissionInventoryReconcile, PermissionInventoryFreshness, PermissionAgentHeartbeatEvaluate, PermissionAgentLeaseEvaluate,
			PermissionCoreObjectsRead, PermissionCoreObjectsWrite,
		}},
		{Name: "auditor", Description: "Read-only security and audit access", Permissions: []Permission{
			PermissionOverviewRead, PermissionAuditRead, PermissionUsersRead, PermissionRolesRead,
			PermissionResourcesRead,
			PermissionActionsRead, PermissionJobsRead, PermissionMarketRead, PermissionNodeLifecycleRead,
			PermissionCoreObjectsRead,
		}},
		{Name: "viewer", Description: "Read-only overview access", Permissions: []Permission{
			PermissionOverviewRead, PermissionResourcesRead, PermissionActionsRead, PermissionMarketRead, PermissionNodeLifecycleRead,
			PermissionCoreObjectsRead,
		}},
	}
}
