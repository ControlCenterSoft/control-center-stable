package market

import "testing"

func TestResolveInstallOrder(t *testing.T) {
	order, err := ResolveInstallOrder([]ModuleDependency{
		{Name: "files", DependsOn: []string{"identity"}},
		{Name: "identity"},
		{Name: "backup", DependsOn: []string{"files"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	positions := map[string]int{}
	for index, name := range order {
		positions[name] = index
	}
	if positions["identity"] > positions["files"] || positions["files"] > positions["backup"] {
		t.Fatalf("invalid dependency order: %#v", order)
	}
}

func TestResolveInstallOrderRejectsMissingDependency(t *testing.T) {
	_, err := ResolveInstallOrder([]ModuleDependency{{Name: "files", DependsOn: []string{"identity"}}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveInstallOrderRejectsCycle(t *testing.T) {
	_, err := ResolveInstallOrder([]ModuleDependency{
		{Name: "a", DependsOn: []string{"b"}},
		{Name: "b", DependsOn: []string{"a"}},
	})
	if err == nil {
		t.Fatal("expected cycle error")
	}
}
