package integration

import "testing"

func TestMissingIsDeterministicAndDeduplicated(t *testing.T) {
	requirement := Requirement{Name: "pxe-automation", Requires: []string{"agent", "inventory", "agent", "pxe"}}
	missing := Missing(requirement, map[string]bool{"agent": true})
	want := []string{"inventory", "pxe"}
	if len(missing) != len(want) {
		t.Fatalf("got %v, want %v", missing, want)
	}
	for i := range want {
		if missing[i] != want[i] {
			t.Fatalf("got %v, want %v", missing, want)
		}
	}
}

func TestCompatible(t *testing.T) {
	requirement := Requirement{Requires: []string{"inventory", "automation"}}
	if !Compatible(requirement, map[string]bool{"inventory": true, "automation": true}) {
		t.Fatal("expected compatible capability set")
	}
}
