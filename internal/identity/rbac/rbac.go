package rbac

import (
	"errors"
	"strings"
	"sync"
)

type Permission string

const (
	PermissionAll            Permission = "*"
	PermissionOverviewRead   Permission = "system.overview.read"
	PermissionUsersRead      Permission = "identity.users.read"
	PermissionUsersWrite     Permission = "identity.users.write"
	PermissionRolesRead      Permission = "identity.roles.read"
	PermissionRolesWrite     Permission = "identity.roles.write"
	PermissionAuditRead      Permission = "audit.events.read"
	PermissionResourcesRead  Permission = "resources.read"
	PermissionRevisionsWrite Permission = "config.revisions.write"
	PermissionActionsRead    Permission = "orchestration.actions.read"
	PermissionChangesWrite   Permission = "orchestration.changes.write"
	PermissionChangesApprove Permission = "orchestration.changes.approve"
	PermissionJobsRead       Permission = "orchestration.jobs.read"
	PermissionJobsCancel     Permission = "orchestration.jobs.cancel"
	PermissionActionsExecute Permission = "orchestration.actions.execute"
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

type Binding struct {
	SubjectID string `json:"subject_id"`
	RoleName  string `json:"role_name"`
	Scope     Scope  `json:"scope"`
}

type Checker interface {
	Allowed(subjectID string, permission Permission, target Scope) bool
}

// Authorizer is deliberately deny-by-default. A permission is granted only by
// an explicit role binding at the exact target scope or at global scope.
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
		}},
		{Name: "auditor", Description: "Read-only security and audit access", Permissions: []Permission{
			PermissionOverviewRead, PermissionAuditRead, PermissionUsersRead, PermissionRolesRead,
			PermissionResourcesRead,
			PermissionActionsRead, PermissionJobsRead,
		}},
		{Name: "viewer", Description: "Read-only overview access", Permissions: []Permission{
			PermissionOverviewRead, PermissionResourcesRead, PermissionActionsRead,
		}},
	}
}
