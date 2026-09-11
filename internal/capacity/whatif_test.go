package capacity

import (
	"errors"
	"testing"

	"control-center/internal/corecontracts"
)

func TestWhatIfSetIsDeterministicAndPrefersFailureReserve(t *testing.T) {
	request := AssessmentRequest{
		ScopeID:             "site-a",
		RequiredRole:        corecontracts.RoleWorkerNode,
		WorkloadUnit:        WorkloadDevices,
		FailureReserveNodes: 0,
		ExpectedWorkload:    100,
	}
	nodes := []NodeProjection{
		projection("node-c", 70, 20, 15),
		projection("node-a", 100, 40, 25),
		projection("node-b", 80, 30, 20),
	}
	scenarios := []WhatIfScenario{
		{ScenarioID: "overload", ExpectedWorkload: 260, FailureReserveNodes: 0},
		{ScenarioID: "ha", ExpectedWorkload: 130, FailureReserveNodes: 1},
		{ScenarioID: "growth", ExpectedWorkload: 180, FailureReserveNodes: 0},
	}

	set, err := BuildWhatIfSet(request, nodes, scenarios)
	if err != nil {
		t.Fatal(err)
	}
	if set.SchemaVersion != WhatIfSetSchemaV1 || !set.AdvisoryOnly || set.ProductionMutation {
		t.Fatalf("what-if set lost advisory boundary: %#v", set)
	}
	if set.BestSafeScenarioID != "ha" {
		t.Fatalf("expected resilient scenario to win, got %q", set.BestSafeScenarioID)
	}
	if len(set.Scenarios) != 3 || set.Scenarios[0].ScenarioID != "growth" || set.Scenarios[1].ScenarioID != "ha" || set.Scenarios[2].ScenarioID != "overload" {
		t.Fatalf("scenarios are not canonicalized: %#v", set.Scenarios)
	}
	if !set.Scenarios[0].Assessment.Safe || !set.Scenarios[1].Assessment.Safe || set.Scenarios[2].Assessment.Safe {
		t.Fatalf("unexpected scenario safety results: %#v", set.Scenarios)
	}

	reversed, err := BuildWhatIfSet(request, []NodeProjection{nodes[2], nodes[0], nodes[1]}, []WhatIfScenario{scenarios[2], scenarios[0], scenarios[1]})
	if err != nil {
		t.Fatal(err)
	}
	if set.BestSafeScenarioID != reversed.BestSafeScenarioID || len(set.Scenarios) != len(reversed.Scenarios) {
		t.Fatalf("what-if result depends on input order: %#v %#v", set, reversed)
	}
	for i := range set.Scenarios {
		if set.Scenarios[i] != reversed.Scenarios[i] {
			t.Fatalf("scenario %d depends on input order: %#v %#v", i, set.Scenarios[i], reversed.Scenarios[i])
		}
	}
}

func TestWhatIfSetRejectsDuplicateScenarioIDs(t *testing.T) {
	request := AssessmentRequest{ScopeID: "site-a", RequiredRole: corecontracts.RoleWorkerNode, WorkloadUnit: WorkloadDevices, ExpectedWorkload: 10}
	_, err := BuildWhatIfSet(request, []NodeProjection{projection("node-a", 100, 10, 50)}, []WhatIfScenario{
		{ScenarioID: "same", ExpectedWorkload: 20},
		{ScenarioID: " same ", ExpectedWorkload: 30},
	})
	if !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("expected duplicate scenario validation error, got %v", err)
	}
}

func TestWhatIfSetFailsClosedWhenScenarioConsumesFleet(t *testing.T) {
	request := AssessmentRequest{ScopeID: "site-a", RequiredRole: corecontracts.RoleWorkerNode, WorkloadUnit: WorkloadDevices, ExpectedWorkload: 10}
	_, err := BuildWhatIfSet(request, []NodeProjection{projection("node-a", 100, 10, 50)}, []WhatIfScenario{
		{ScenarioID: "reserve-all", ExpectedWorkload: 20, FailureReserveNodes: 1},
	})
	if !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("expected scenario validation error, got %v", err)
	}
}
