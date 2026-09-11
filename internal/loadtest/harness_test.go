package loadtest

import (
	"errors"
	"testing"
)

func TestHarnessExercisesAgentPopulationWithoutProductionTraffic(t *testing.T) {
	report, err := Run(Config{AgentCount: 10_000, Rounds: 12, BatchSize: 256, RestartEvery: 4, DropEvery: 97})
	if err != nil {
		t.Fatal(err)
	}
	if report.OfferedHeartbeats != 120_000 || report.ControllerRestarts != 2 || report.RejectedStaleResults == 0 {
		t.Fatalf("missing load/restart evidence: %#v", report)
	}
	if report.FalseSuccesses != 0 || !report.SideEffectFree || report.ProductionTraffic {
		t.Fatalf("unsafe harness report: %#v", report)
	}
	if !report.Baseline.Synthetic || report.Baseline.Confidence != "certified" || report.Baseline.PeakBatch != 256 {
		t.Fatalf("bad capacity baseline: %#v", report.Baseline)
	}
}

func TestHarnessIsDeterministic(t *testing.T) {
	config := Config{AgentCount: 101, Rounds: 9, BatchSize: 17, RestartEvery: 3, DropEvery: 11}
	first, err := Run(config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Run(config)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("reports differ: %#v %#v", first, second)
	}
}

func TestHarnessRejectsUnsafeBounds(t *testing.T) {
	_, err := Run(Config{AgentCount: 100_001, Rounds: 1, BatchSize: 1})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected validation error, got %v", err)
	}
}
