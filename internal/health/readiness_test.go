package health

import "testing"

func TestSummarizeReadiness(t *testing.T) {
	result := SummarizeReadiness([]ReadinessCheck{
		{Name: "storage", Ready: false},
		{Name: "database", Ready: false},
		{Name: "api", Ready: true},
	})
	if result.Ready {
		t.Fatal("expected readiness failure")
	}
	if len(result.Failed) != 2 || result.Failed[0] != "database" || result.Failed[1] != "storage" {
		t.Fatalf("unexpected failed checks: %#v", result.Failed)
	}
}
