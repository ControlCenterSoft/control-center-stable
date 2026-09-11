package capacity

import (
	"fmt"
	"sort"
	"strings"
)

const WhatIfSetSchemaV1 = "capacity.what-if-set/v1"

// WhatIfScenario describes one advisory capacity scenario. It changes only
// planner inputs and never carries an executable infrastructure operation.
type WhatIfScenario struct {
	ScenarioID          string  `json:"scenario_id"`
	ExpectedWorkload    float64 `json:"expected_workload"`
	FailureReserveNodes int     `json:"failure_reserve_nodes"`
}

// WhatIfResult contains the deterministic assessment for one scenario and its
// delta from the baseline request.
type WhatIfResult struct {
	ScenarioID            string     `json:"scenario_id"`
	ExpectedWorkloadDelta float64    `json:"expected_workload_delta"`
	SafeReserveDelta      float64    `json:"safe_reserve_delta"`
	Assessment            Assessment `json:"assessment"`
}

// WhatIfSet groups a baseline assessment with independently evaluated
// scenarios. BestSafeScenarioID prefers stronger failure reserve first, then
// the highest supported workload. The result remains advisory-only.
type WhatIfSet struct {
	SchemaVersion      string         `json:"schema_version"`
	Baseline           Assessment     `json:"baseline"`
	Scenarios          []WhatIfResult `json:"scenarios"`
	BestSafeScenarioID string         `json:"best_safe_scenario_id,omitempty"`
	AdvisoryOnly       bool           `json:"advisory_only"`
	ProductionMutation bool           `json:"production_mutation"`
}

// BuildWhatIfSet evaluates a bounded set of capacity scenarios against one
// immutable node projection. Scenario ordering does not affect the result.
func BuildWhatIfSet(baselineRequest AssessmentRequest, nodes []NodeProjection, scenarios []WhatIfScenario) (WhatIfSet, error) {
	baseline, err := BuildAssessment(baselineRequest, nodes)
	if err != nil {
		return WhatIfSet{}, fmt.Errorf("baseline assessment: %w", err)
	}
	if len(scenarios) == 0 || len(scenarios) > 128 {
		return WhatIfSet{}, fmt.Errorf("%w: scenarios must contain between 1 and 128 items", ErrInvalidRecommendation)
	}

	normalized := make([]WhatIfScenario, len(scenarios))
	seen := make(map[string]struct{}, len(scenarios))
	for i, scenario := range scenarios {
		scenario.ScenarioID = strings.TrimSpace(scenario.ScenarioID)
		if !identifierPattern.MatchString(scenario.ScenarioID) {
			return WhatIfSet{}, fmt.Errorf("%w: invalid scenario_id %q", ErrInvalidRecommendation, scenario.ScenarioID)
		}
		if _, duplicate := seen[scenario.ScenarioID]; duplicate {
			return WhatIfSet{}, fmt.Errorf("%w: duplicate scenario_id %q", ErrInvalidRecommendation, scenario.ScenarioID)
		}
		if !finite(scenario.ExpectedWorkload) || scenario.ExpectedWorkload < 0 {
			return WhatIfSet{}, fmt.Errorf("%w: invalid expected workload for scenario %q", ErrInvalidRecommendation, scenario.ScenarioID)
		}
		if scenario.FailureReserveNodes < 0 || scenario.FailureReserveNodes > 16 {
			return WhatIfSet{}, fmt.Errorf("%w: invalid failure reserve for scenario %q", ErrInvalidRecommendation, scenario.ScenarioID)
		}
		seen[scenario.ScenarioID] = struct{}{}
		normalized[i] = scenario
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].ScenarioID < normalized[j].ScenarioID })

	results := make([]WhatIfResult, 0, len(normalized))
	bestID := ""
	bestReserveNodes := -1
	bestExpectedWorkload := -1.0
	bestSafeReserve := -1.0
	for _, scenario := range normalized {
		request := baselineRequest
		request.ExpectedWorkload = scenario.ExpectedWorkload
		request.FailureReserveNodes = scenario.FailureReserveNodes
		assessment, err := BuildAssessment(request, nodes)
		if err != nil {
			return WhatIfSet{}, fmt.Errorf("scenario %q: %w", scenario.ScenarioID, err)
		}
		results = append(results, WhatIfResult{
			ScenarioID:            scenario.ScenarioID,
			ExpectedWorkloadDelta: scenario.ExpectedWorkload - baseline.ExpectedWorkload,
			SafeReserveDelta:      assessment.SafeReserve - baseline.SafeReserve,
			Assessment:            assessment,
		})

		if !assessment.Safe {
			continue
		}
		better := scenario.FailureReserveNodes > bestReserveNodes ||
			(scenario.FailureReserveNodes == bestReserveNodes && scenario.ExpectedWorkload > bestExpectedWorkload+floatTolerance(scenario.ExpectedWorkload, bestExpectedWorkload)) ||
			(scenario.FailureReserveNodes == bestReserveNodes && closeFloat(scenario.ExpectedWorkload, bestExpectedWorkload) && assessment.SafeReserve > bestSafeReserve+floatTolerance(assessment.SafeReserve, bestSafeReserve)) ||
			(scenario.FailureReserveNodes == bestReserveNodes && closeFloat(scenario.ExpectedWorkload, bestExpectedWorkload) && closeFloat(assessment.SafeReserve, bestSafeReserve) && (bestID == "" || scenario.ScenarioID < bestID))
		if better {
			bestID = scenario.ScenarioID
			bestReserveNodes = scenario.FailureReserveNodes
			bestExpectedWorkload = scenario.ExpectedWorkload
			bestSafeReserve = assessment.SafeReserve
		}
	}

	return WhatIfSet{
		SchemaVersion:      WhatIfSetSchemaV1,
		Baseline:           baseline,
		Scenarios:          results,
		BestSafeScenarioID: bestID,
		AdvisoryOnly:       true,
		ProductionMutation: false,
	}, nil
}
