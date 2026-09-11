package automation

import "testing"

func TestBuildPlanSelectsPlatformAdapter(t *testing.T) {
	linux, err := BuildPlan(Request{
		Target:    Target{ID: "node-linux", Platform: "linux"},
		Operation: EnsurePackage,
		Arguments: map[string]string{"name": "example-package", "state": "present"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if linux.Adapter != "ansible.linux" || linux.Action != EnsurePackage {
		t.Fatalf("linux plan = %#v", linux)
	}

	windows, err := BuildPlan(Request{
		Target:    Target{ID: "node-windows", Platform: "windows"},
		Operation: EnsureService,
		Arguments: map[string]string{"name": "ExampleService", "state": "started"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if windows.Adapter != "ansible.windows" {
		t.Fatalf("windows adapter = %q", windows.Adapter)
	}
}

func TestBuildPlanRejectsArbitraryCommandShape(t *testing.T) {
	_, err := BuildPlan(Request{
		Target:    Target{ID: "node-1", Platform: "linux"},
		Operation: EnsureService,
		Arguments: map[string]string{"name": "example", "command": "arbitrary-value"},
	})
	if err == nil {
		t.Fatal("unexpected command argument accepted")
	}
}
