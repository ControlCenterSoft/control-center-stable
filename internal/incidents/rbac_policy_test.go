package incidents

import (
	"context"
	"errors"
	"testing"

	"control-center/internal/corecontracts"
	"control-center/internal/identity/rbac"
)

type recordingRBACChecker struct {
	allowed    bool
	subjectID  string
	permission rbac.Permission
	target     rbac.Scope
}

func (c *recordingRBACChecker) Allowed(subjectID string, permission rbac.Permission, target rbac.Scope) bool {
	c.subjectID = subjectID
	c.permission = permission
	c.target = target
	return c.allowed
}

type failingRBACScopeResolver struct{}

func (failingRBACScopeResolver) ListScope(context.Context, ListQuery) (rbac.Scope, error) {
	return rbac.Scope{}, ErrIncidentRBACScopeUnavailable
}

func (failingRBACScopeResolver) IncidentScope(context.Context, Incident) (rbac.Scope, error) {
	return rbac.Scope{}, ErrIncidentRBACScopeUnavailable
}

func incidentWithScope(scopeID string) Incident {
	return Incident{ObjectMetadata: corecontracts.ObjectMetadata{ScopeID: scopeID}}
}

func TestDefaultRBACPermissionMapUsesExistingLeastPrivilegeBoundaries(t *testing.T) {
	permissions := DefaultRBACPermissionMap()
	if permissions.Read != rbac.PermissionResourcesRead || permissions.List != rbac.PermissionResourcesRead {
		t.Fatalf("incident reads must use resources.read: %#v", permissions)
	}
	if permissions.Acknowledge != rbac.PermissionCoreObjectsWrite || permissions.Resolve != rbac.PermissionCoreObjectsWrite || permissions.EvidenceUpdate != rbac.PermissionCoreObjectsWrite {
		t.Fatalf("incident mutations must use core.objects.write: %#v", permissions)
	}
	if err := permissions.Validate(); err != nil {
		t.Fatalf("default permission map must validate: %v", err)
	}
}

func TestRBACOperatorPolicyAuthorizesIncidentAgainstResolvedScope(t *testing.T) {
	checker := &recordingRBACChecker{allowed: true}
	policy, err := NewRBACOperatorPolicy(checker, ScopeIDRBACResolver{Kind: rbac.ScopeSite}, DefaultRBACPermissionMap())
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}

	if err := policy.AuthorizeIncident(context.Background(), "operator-1", CapabilityIncidentRead, incidentWithScope("site-a")); err != nil {
		t.Fatalf("authorize read: %v", err)
	}
	if checker.subjectID != "operator-1" {
		t.Fatalf("unexpected subject: %q", checker.subjectID)
	}
	if checker.permission != rbac.PermissionResourcesRead {
		t.Fatalf("unexpected permission: %q", checker.permission)
	}
	want := rbac.Scope{Kind: rbac.ScopeSite, ID: "site-a"}
	if checker.target != want {
		t.Fatalf("unexpected target: %#v want %#v", checker.target, want)
	}
}

func TestRBACOperatorPolicyDeniesMutationWithoutGrant(t *testing.T) {
	checker := &recordingRBACChecker{allowed: false}
	policy, err := NewRBACOperatorPolicy(checker, ScopeIDRBACResolver{Kind: rbac.ScopeTenant}, DefaultRBACPermissionMap())
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}

	err = policy.AuthorizeIncident(context.Background(), "operator-1", CapabilityIncidentAcknowledge, incidentWithScope("tenant-a"))
	if !errors.Is(err, ErrOperatorAccessDenied) {
		t.Fatalf("expected access denied, got %v", err)
	}
	if checker.permission != rbac.PermissionCoreObjectsWrite {
		t.Fatalf("unexpected permission: %q", checker.permission)
	}
}

func TestRBACOperatorPolicyRequiresGlobalGrantForUnscopedList(t *testing.T) {
	checker := &recordingRBACChecker{allowed: true}
	policy, err := NewRBACOperatorPolicy(checker, ScopeIDRBACResolver{Kind: rbac.ScopeSite}, DefaultRBACPermissionMap())
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}

	if err := policy.AuthorizeList(context.Background(), "auditor-1", CapabilityIncidentList, ListQuery{}); err != nil {
		t.Fatalf("authorize list: %v", err)
	}
	if checker.target != rbac.GlobalScope() {
		t.Fatalf("unscoped list must require global scope, got %#v", checker.target)
	}
	if checker.permission != rbac.PermissionResourcesRead {
		t.Fatalf("unexpected permission: %q", checker.permission)
	}
}

func TestRBACOperatorPolicyFailsClosedWhenScopeCannotBeResolved(t *testing.T) {
	checker := &recordingRBACChecker{allowed: true}
	policy, err := NewRBACOperatorPolicy(checker, failingRBACScopeResolver{}, DefaultRBACPermissionMap())
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}

	err = policy.AuthorizeIncident(context.Background(), "operator-1", CapabilityIncidentResolve, incidentWithScope("site-a"))
	if !errors.Is(err, ErrOperatorDependencyUnavailable) {
		t.Fatalf("expected dependency failure, got %v", err)
	}
	if checker.permission != "" {
		t.Fatalf("checker must not be called after scope resolution failure")
	}
}

func TestRBACOperatorPolicyRejectsUnsupportedCapability(t *testing.T) {
	checker := &recordingRBACChecker{allowed: true}
	policy, err := NewRBACOperatorPolicy(checker, ScopeIDRBACResolver{Kind: rbac.ScopeSite}, DefaultRBACPermissionMap())
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}

	err = policy.AuthorizeIncident(context.Background(), "operator-1", OperatorCapability("incidents.delete"), incidentWithScope("site-a"))
	if !errors.Is(err, ErrOperatorDependencyUnavailable) {
		t.Fatalf("expected dependency failure for unsupported capability, got %v", err)
	}
	if checker.permission != "" {
		t.Fatalf("checker must not be called for unsupported capability")
	}
}
