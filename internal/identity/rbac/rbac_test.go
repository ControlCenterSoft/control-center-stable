package rbac

import "testing"

func TestDenyByDefault(t *testing.T) {
	a := NewAuthorizer()
	if a.Allowed("user-1", PermissionOverviewRead, GlobalScope()) {
		t.Fatal("permission granted without role or binding")
	}
	for _, role := range BuiltinRoles() {
		if err := a.RegisterRole(role); err != nil {
			t.Fatal(err)
		}
	}
	if a.Allowed("user-1", PermissionOverviewRead, GlobalScope()) {
		t.Fatal("permission granted without explicit binding")
	}
}

func TestExactScopeAndGlobalScope(t *testing.T) {
	a := NewAuthorizer()
	if err := a.RegisterRole(Role{Name: "site-viewer", Permissions: []Permission{PermissionOverviewRead}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Bind(Binding{SubjectID: "user-1", RoleName: "site-viewer", Scope: Scope{Kind: ScopeSite, ID: "site-a"}}); err != nil {
		t.Fatal(err)
	}
	if !a.Allowed("user-1", PermissionOverviewRead, Scope{Kind: ScopeSite, ID: "site-a"}) {
		t.Fatal("exact scoped permission denied")
	}
	if a.Allowed("user-1", PermissionOverviewRead, Scope{Kind: ScopeSite, ID: "site-b"}) {
		t.Fatal("binding escaped its scope")
	}
	if a.Allowed("user-1", PermissionUsersWrite, Scope{Kind: ScopeSite, ID: "site-a"}) {
		t.Fatal("unassigned permission granted")
	}
}

func TestAdministratorStillRequiresBinding(t *testing.T) {
	a := NewAuthorizer()
	for _, role := range BuiltinRoles() {
		if err := a.RegisterRole(role); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Bind(Binding{SubjectID: "admin-1", RoleName: "administrator", Scope: GlobalScope()}); err != nil {
		t.Fatal(err)
	}
	if !a.Allowed("admin-1", PermissionRolesWrite, Scope{Kind: ScopeResource, ID: "resource-1"}) {
		t.Fatal("global administrator binding did not authorize target")
	}
	if a.Allowed("other", PermissionRolesWrite, GlobalScope()) {
		t.Fatal("administrator privilege leaked to another subject")
	}
}
