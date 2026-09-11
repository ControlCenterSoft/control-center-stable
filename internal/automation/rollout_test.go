package automation

import "testing"

func TestBuildRolloutWaves(t *testing.T) {
	waves, err := BuildRolloutWaves([]string{"node-c", "node-a", "node-b", "node-d"}, RolloutStrategy{CanarySize: 1, MaxParallel: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := len(waves), 3; got != want {
		t.Fatalf("wave count=%d want=%d", got, want)
	}
	if waves[0][0] != "node-a" {
		t.Fatalf("unexpected canary wave: %#v", waves[0])
	}
	if len(waves[1]) != 2 || waves[1][0] != "node-b" || waves[1][1] != "node-c" {
		t.Fatalf("unexpected second wave: %#v", waves[1])
	}
}

func TestBuildRolloutWavesDeduplicatesTargets(t *testing.T) {
	waves, err := BuildRolloutWaves([]string{"node-a", "node-a"}, RolloutStrategy{MaxParallel: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(waves) != 1 || len(waves[0]) != 1 {
		t.Fatalf("unexpected waves: %#v", waves)
	}
}

func TestBuildRolloutWavesRejectsInvalidParallelism(t *testing.T) {
	_, err := BuildRolloutWaves([]string{"node-a"}, RolloutStrategy{MaxParallel: 0})
	if err == nil {
		t.Fatal("expected error")
	}
}
