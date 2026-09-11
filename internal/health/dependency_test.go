package health

import "testing"

func TestEvaluateDependencyReadiness(t *testing.T) {
	result, err := EvaluateDependencyReadiness([]ServiceDependencyState{
		{Name: "api", Healthy: true, Dependencies: []string{"db"}},
		{Name: "db", Healthy: true},
		{Name: "worker", Healthy: false, Dependencies: []string{"db"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := len(result.Ready), 2; got != want {
		t.Fatalf("ready count=%d want=%d", got, want)
	}
	if result.Ready[0] != "api" || result.Ready[1] != "db" {
		t.Fatalf("unexpected ready order: %#v", result.Ready)
	}
	if got, want := len(result.Blocked), 1; got != want || result.Blocked[0] != "worker" {
		t.Fatalf("unexpected blocked services: %#v", result.Blocked)
	}
}

func TestEvaluateDependencyReadinessBlocksOnUnhealthyDependency(t *testing.T) {
	result, err := EvaluateDependencyReadiness([]ServiceDependencyState{
		{Name: "api", Healthy: true, Dependencies: []string{"db"}},
		{Name: "db", Healthy: false},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := len(result.Blocked), 2; got != want {
		t.Fatalf("blocked count=%d want=%d: %#v", got, want, result.Blocked)
	}
}

func TestEvaluateDependencyReadinessRejectsUnknownDependency(t *testing.T) {
	_, err := EvaluateDependencyReadiness([]ServiceDependencyState{{Name: "api", Healthy: true, Dependencies: []string{"db"}}})
	if err == nil {
		t.Fatal("expected error")
	}
}
