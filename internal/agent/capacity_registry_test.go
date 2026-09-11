package agent

import (
	"math"
	"testing"
)

func TestCapacityMetricRegistryIsCompleteAndBounded(t *testing.T) {
	want := map[CapacityMetric]CapacityMetricDefinition{
		MetricCPUUtilization:       {Unit: UnitPercent, TargetKind: CapacityTargetNode, Maximum: 100},
		MetricMemoryUsed:           {Unit: UnitBytes, TargetKind: CapacityTargetNode, Maximum: 1e21},
		MetricStorageUsed:          {Unit: UnitBytes, TargetKind: CapacityTargetStorage, Maximum: 1e21},
		MetricStorageIOPS:          {Unit: UnitOperationsPerSecond, TargetKind: CapacityTargetStorage, Maximum: 1e21},
		MetricStorageLatency:       {Unit: UnitMilliseconds, TargetKind: CapacityTargetStorage, Maximum: 1e21},
		MetricStorageQueueDepth:    {Unit: UnitCount, TargetKind: CapacityTargetStorage, Maximum: 1e21},
		MetricNetworkThroughput:    {Unit: UnitBitsPerSecond, TargetKind: CapacityTargetNetwork, Maximum: 1e21},
		MetricNetworkLatency:       {Unit: UnitMilliseconds, TargetKind: CapacityTargetNetwork, Maximum: 1e21},
		MetricNetworkPacketLoss:    {Unit: UnitPercent, TargetKind: CapacityTargetNetwork, Maximum: 100},
		MetricDatabaseLatency:      {Unit: UnitMilliseconds, TargetKind: CapacityTargetDatabase, Maximum: 1e21},
		MetricDatabaseTransactions: {Unit: UnitTransactionsPerSecond, TargetKind: CapacityTargetDatabase, Maximum: 1e21},
		MetricServiceQueueDepth:    {Unit: UnitCount, TargetKind: CapacityTargetService, Maximum: 1e21},
	}
	if len(capacityMetricDefinitions) != len(want) {
		t.Fatalf("metric registry has %d definitions, want %d", len(capacityMetricDefinitions), len(want))
	}
	for metric, expected := range want {
		definition, exists := DefinitionForCapacityMetric(metric)
		if !exists || definition != expected {
			t.Errorf("DefinitionForCapacityMetric(%q) = %#v, %v; want %#v, true", metric, definition, exists, expected)
		}
		if !ValidCapacityMetricValue(metric, 0) || !ValidCapacityMetricValue(metric, expected.Maximum) {
			t.Errorf("metric %q rejected canonical bounds", metric)
		}
		if ValidCapacityMetricValue(metric, -1) || ValidCapacityMetricValue(metric, expected.Maximum*1.01) ||
			ValidCapacityMetricValue(metric, math.NaN()) || ValidCapacityMetricValue(metric, math.Inf(1)) {
			t.Errorf("metric %q accepted unsafe value", metric)
		}
	}
	if _, exists := DefinitionForCapacityMetric("custom.metric"); exists || ValidCapacityMetricValue("custom.metric", 1) {
		t.Fatal("unknown metric accepted")
	}
}
