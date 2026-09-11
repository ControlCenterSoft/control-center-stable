package health

import "testing"

func TestAggregateUsesWorstStateAndStableOrder(t *testing.T) {
	summary := Aggregate([]Check{
		{Name: "storage", State: StateHealthy},
		{Name: "api", State: StateDegraded},
		{Name: "database", State: StateUnhealthy},
	})
	if summary.State != StateUnhealthy {
		t.Fatalf("state = %q, want %q", summary.State, StateUnhealthy)
	}
	got := []string{summary.Checks[0].Name, summary.Checks[1].Name, summary.Checks[2].Name}
	want := []string{"api", "database", "storage"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAggregateEmptyIsUnknown(t *testing.T) {
	if got := Aggregate(nil).State; got != StateUnknown {
		t.Fatalf("state = %q, want %q", got, StateUnknown)
	}
}
