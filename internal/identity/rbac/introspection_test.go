package rbac

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestEffectiveGrantsAreDeterministicAndSubjectScoped(t *testing.T) {
	a := NewAuthorizer()
	for _, role := range []Role{
		{Name: "site-reader", Permissions: []Permission{PermissionResourcesRead, PermissionOverviewRead}},
		{Name: "tenant-auditor", Permissions: []Permission{PermissionAuditRead}},
	} {
		if err := a.RegisterRole(role); err != nil {
			t.Fatal(err)
		}
	}
	for _, binding := range []Binding{
		{SubjectID: "other", RoleName: "tenant-auditor", Scope: Scope{Kind: ScopeTenant, ID: "tenant-z"}},
		{SubjectID: "user-1", RoleName: "site-reader", Scope: Scope{Kind: ScopeSite, ID: "site-b"}},
		{SubjectID: "user-1", RoleName: "tenant-auditor", Scope: Scope{Kind: ScopeTenant, ID: "tenant-a"}},
	} {
		if err := a.Bind(binding); err != nil {
			t.Fatal(err)
		}
	}

	got, err := a.EffectiveGrants(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	want := []EffectiveGrant{
		{RoleName: "site-reader", Scope: Scope{Kind: ScopeSite, ID: "site-b"}, Permissions: []Permission{PermissionResourcesRead, PermissionOverviewRead}},
		{RoleName: "tenant-auditor", Scope: Scope{Kind: ScopeTenant, ID: "tenant-a"}, Permissions: []Permission{PermissionAuditRead}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("grants=%#v, want %#v", got, want)
	}

	other, err := a.EffectiveGrants(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 1 || other[0].RoleName != "tenant-auditor" || other[0].Scope.ID != "tenant-z" {
		t.Fatalf("subject isolation failed: %#v", other)
	}
}

func TestEffectiveGrantsHonorContextAndRejectBlankSubject(t *testing.T) {
	a := NewAuthorizer()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.EffectiveGrants(ctx, "user-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context error=%v, want context.Canceled", err)
	}
	if _, err := a.EffectiveGrants(context.Background(), "   "); err == nil {
		t.Fatal("blank subject unexpectedly accepted")
	}
}
