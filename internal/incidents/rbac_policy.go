package incidents

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"control-center/internal/identity/rbac"
)

var ErrIncidentRBACScopeUnavailable = errors.New("incident RBAC scope unavailable")

// RBACPermissionMap maps incident application capabilities onto the canonical
// Control Center RBAC permission model. The default mapping intentionally uses
// existing persisted permissions so this adapter can be adopted without
// inventing a second authorization store or requiring an unsafe implicit grant.
type RBACPermissionMap struct {
	Read           rbac.Permission
	List           rbac.Permission
	Acknowledge    rbac.Permission
	Resolve        rbac.Permission
	EvidenceUpdate rbac.Permission
}

// DefaultRBACPermissionMap keeps incident reads inside the existing resource
// read boundary and incident mutations inside the distributed Core object write
// boundary. A future schema migration may split these into narrower permissions
// without changing OperatorService itself.
func DefaultRBACPermissionMap() RBACPermissionMap {
	return RBACPermissionMap{
		Read:           rbac.PermissionResourcesRead,
		List:           rbac.PermissionResourcesRead,
		Acknowledge:    rbac.PermissionCoreObjectsWrite,
		Resolve:        rbac.PermissionCoreObjectsWrite,
		EvidenceUpdate: rbac.PermissionCoreObjectsWrite,
	}
}

func (m RBACPermissionMap) Validate() error {
	permissions := []rbac.Permission{m.Read, m.List, m.Acknowledge, m.Resolve, m.EvidenceUpdate}
	for _, permission := range permissions {
		if strings.TrimSpace(string(permission)) == "" {
			return errors.New("incident RBAC permission map contains an empty permission")
		}
	}
	return nil
}

// RBACScopeResolver translates the incident Core scope into the deployment's
// canonical RBAC scope model. Implementations must never infer a broader scope
// when a concrete incident scope cannot be resolved.
type RBACScopeResolver interface {
	ListScope(context.Context, ListQuery) (rbac.Scope, error)
	IncidentScope(context.Context, Incident) (rbac.Scope, error)
}

// ScopeIDRBACResolver is the safe default for deployments where incident
// ScopeID values correspond directly to one RBAC scope kind. Unscoped list
// queries require a global grant; they are never broadened to an arbitrary
// tenant/site/resource scope.
type ScopeIDRBACResolver struct {
	Kind rbac.ScopeKind
}

func (r ScopeIDRBACResolver) ListScope(ctx context.Context, query ListQuery) (rbac.Scope, error) {
	if err := ctx.Err(); err != nil {
		return rbac.Scope{}, err
	}
	if strings.TrimSpace(query.ScopeID) == "" {
		return rbac.GlobalScope(), nil
	}
	return r.resolve(query.ScopeID)
}

func (r ScopeIDRBACResolver) IncidentScope(ctx context.Context, incident Incident) (rbac.Scope, error) {
	if err := ctx.Err(); err != nil {
		return rbac.Scope{}, err
	}
	return r.resolve(incident.ScopeID)
}

func (r ScopeIDRBACResolver) resolve(scopeID string) (rbac.Scope, error) {
	scopeID = strings.TrimSpace(scopeID)
	if scopeID == "" || r.Kind == rbac.ScopeGlobal {
		return rbac.Scope{}, ErrIncidentRBACScopeUnavailable
	}
	scope := rbac.Scope{Kind: r.Kind, ID: scopeID}
	if !scope.Valid() {
		return rbac.Scope{}, ErrIncidentRBACScopeUnavailable
	}
	return scope, nil
}

// RBACOperatorPolicy adapts the persisted Control Center RBAC checker to the
// incident OperatorAccessPolicy contract. Every dependency or scope-resolution
// failure is fail-closed; authorization errors never become implicit grants.
type RBACOperatorPolicy struct {
	checker     rbac.Checker
	scopes      RBACScopeResolver
	permissions RBACPermissionMap
}

func NewRBACOperatorPolicy(checker rbac.Checker, scopes RBACScopeResolver, permissions RBACPermissionMap) (*RBACOperatorPolicy, error) {
	if checker == nil || scopes == nil {
		return nil, ErrOperatorDependencyUnavailable
	}
	if err := permissions.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOperatorDependencyUnavailable, err)
	}
	return &RBACOperatorPolicy{checker: checker, scopes: scopes, permissions: permissions}, nil
}

func (p *RBACOperatorPolicy) AuthorizeList(ctx context.Context, actorID string, capability OperatorCapability, query ListQuery) error {
	if p == nil || p.checker == nil || p.scopes == nil {
		return ErrOperatorDependencyUnavailable
	}
	if capability != CapabilityIncidentList {
		return fmt.Errorf("%w: unsupported list capability %q", ErrOperatorDependencyUnavailable, capability)
	}
	if err := validateOperatorActor(actorID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := p.scopes.ListScope(ctx, query)
	if err != nil || !target.Valid() {
		return fmt.Errorf("%w: %v", ErrOperatorDependencyUnavailable, ErrIncidentRBACScopeUnavailable)
	}
	if !p.checker.Allowed(actorID, p.permissions.List, target) {
		return ErrOperatorAccessDenied
	}
	return nil
}

func (p *RBACOperatorPolicy) AuthorizeIncident(ctx context.Context, actorID string, capability OperatorCapability, incident Incident) error {
	if p == nil || p.checker == nil || p.scopes == nil {
		return ErrOperatorDependencyUnavailable
	}
	if err := validateOperatorActor(actorID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	permission, err := p.permissionFor(capability)
	if err != nil {
		return err
	}
	target, err := p.scopes.IncidentScope(ctx, incident)
	if err != nil || !target.Valid() {
		return fmt.Errorf("%w: %v", ErrOperatorDependencyUnavailable, ErrIncidentRBACScopeUnavailable)
	}
	if !p.checker.Allowed(actorID, permission, target) {
		return ErrOperatorAccessDenied
	}
	return nil
}

func (p *RBACOperatorPolicy) permissionFor(capability OperatorCapability) (rbac.Permission, error) {
	switch capability {
	case CapabilityIncidentRead:
		return p.permissions.Read, nil
	case CapabilityIncidentAcknowledge:
		return p.permissions.Acknowledge, nil
	case CapabilityIncidentResolve:
		return p.permissions.Resolve, nil
	case CapabilityIncidentEvidenceUpdate:
		return p.permissions.EvidenceUpdate, nil
	default:
		return "", fmt.Errorf("%w: unsupported incident capability %q", ErrOperatorDependencyUnavailable, capability)
	}
}
