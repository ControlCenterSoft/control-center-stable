package capacity

import "testing"

func TestEvaluateWorkloadScaleOptionsDeterministicAndSelectsMinimumSafeFactor(t *testing.T) {
	curve, efficiency := workloadScaleEvidence(t, 0.5)
	requests := []WorkloadScaleScenarioRequest{
		{TargetResourceFactor: 4, RequiredWorkload: 150, MinimumHeadroomPercent: 10},
		{TargetResourceFactor: 1, RequiredWorkload: 150, MinimumHeadroomPercent: 10},
		{TargetResourceFactor: 2, RequiredWorkload: 150, MinimumHeadroomPercent: 10},
	}
	first, err := EvaluateWorkloadScaleOptions(curve, efficiency, requests)
	if err != nil {
		t.Fatal(err)
	}
	reversed := []WorkloadScaleScenarioRequest{requests[2], requests[1], requests[0]}
	second, err := EvaluateWorkloadScaleOptions(curve, efficiency, reversed)
	if err != nil {
		t.Fatal(err)
	}
	if first.OptionsID != second.OptionsID {
		t.Fatalf("option IDs differ: %q != %q", first.OptionsID, second.OptionsID)
	}
	if first.Status != WorkloadScaleOptionsRecommendationAvailable || first.RecommendedAction != "none" {
		t.Fatalf("unexpected options result: %#v", first)
	}
	if first.RecommendedResourceFactor == nil || !closeFloat(*first.RecommendedResourceFactor, 2) {
		t.Fatalf("expected resource factor 2, got %#v", first.RecommendedResourceFactor)
	}
	if first.RecommendedScenarioID != first.Options[1].ScenarioID {
		t.Fatalf("recommended scenario is not the minimum safe option: %#v", first)
	}
	if !first.AdvisoryOnly || first.ProductionMutation {
		t.Fatalf("unsafe options flags: %#v", first)
	}
	for index, option := range first.Options {
		if index > 0 && option.Request.TargetResourceFactor < first.Options[index-1].Request.TargetResourceFactor {
			t.Fatalf("options are not sorted by resource factor: %#v", first.Options)
		}
	}
}

func TestEvaluateWorkloadScaleOptionsPreservesDiminishingReturns(t *testing.T) {
	curve, efficiency := workloadScaleEvidence(t, 0.8)
	result, err := EvaluateWorkloadScaleOptions(curve, efficiency, []WorkloadScaleScenarioRequest{
		{TargetResourceFactor: 2, RequiredWorkload: 100, MinimumHeadroomPercent: 10},
		{TargetResourceFactor: 4, RequiredWorkload: 100, MinimumHeadroomPercent: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != WorkloadScaleOptionsDiminishingReturns || result.RecommendedAction != "inspect-bottleneck-before-scaling" {
		t.Fatalf("expected diminishing-returns recommendation: %#v", result)
	}
	if result.RecommendedResourceFactor == nil || !closeFloat(*result.RecommendedResourceFactor, 2) {
		t.Fatalf("unexpected resource factor: %#v", result.RecommendedResourceFactor)
	}
}

func TestEvaluateWorkloadScaleOptionsCollectsEvidenceWithoutExtrapolation(t *testing.T) {
	curve, efficiency := workloadScaleEvidence(t, 0.5)
	result, err := EvaluateWorkloadScaleOptions(curve, efficiency, []WorkloadScaleScenarioRequest{
		{TargetResourceFactor: 1, RequiredWorkload: 150, MinimumHeadroomPercent: 10},
		{TargetResourceFactor: 8, RequiredWorkload: 150, MinimumHeadroomPercent: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != WorkloadScaleOptionsCollectEvidence || result.RecommendedAction != "collect-evidence" {
		t.Fatalf("expected collect-evidence result: %#v", result)
	}
	if result.RecommendedResourceFactor != nil || result.RecommendedScenarioID != "" {
		t.Fatalf("collect-evidence must not select a factor: %#v", result)
	}
}

func TestEvaluateWorkloadScaleOptionsReportsInsufficientCapacity(t *testing.T) {
	curve, efficiency := workloadScaleEvidence(t, 0.5)
	result, err := EvaluateWorkloadScaleOptions(curve, efficiency, []WorkloadScaleScenarioRequest{
		{TargetResourceFactor: 1, RequiredWorkload: 275, MinimumHeadroomPercent: 10},
		{TargetResourceFactor: 2, RequiredWorkload: 275, MinimumHeadroomPercent: 10},
		{TargetResourceFactor: 4, RequiredWorkload: 275, MinimumHeadroomPercent: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != WorkloadScaleOptionsInsufficient || result.RecommendedAction != "increase-evidence-backed-capacity-or-reduce-demand" {
		t.Fatalf("expected insufficient-capacity result: %#v", result)
	}
}

func TestEvaluateWorkloadScaleOptionsRejectsDuplicateResourceFactor(t *testing.T) {
	curve, efficiency := workloadScaleEvidence(t, 0.5)
	if _, err := EvaluateWorkloadScaleOptions(curve, efficiency, []WorkloadScaleScenarioRequest{
		{TargetResourceFactor: 2, RequiredWorkload: 100, MinimumHeadroomPercent: 10},
		{TargetResourceFactor: 2, RequiredWorkload: 100, MinimumHeadroomPercent: 10},
	}); err == nil {
		t.Fatal("expected duplicate resource factor to be rejected")
	}
}

func TestEvaluateWorkloadScaleOptionsRejectsMixedDemandTargets(t *testing.T) {
	curve, efficiency := workloadScaleEvidence(t, 0.5)
	if _, err := EvaluateWorkloadScaleOptions(curve, efficiency, []WorkloadScaleScenarioRequest{
		{TargetResourceFactor: 2, RequiredWorkload: 100, MinimumHeadroomPercent: 10},
		{TargetResourceFactor: 4, RequiredWorkload: 101, MinimumHeadroomPercent: 10},
	}); err == nil {
		t.Fatal("expected mixed demand targets to be rejected")
	}
}

func TestEvaluateWorkloadScaleOptionsRejectsTamperedEfficiencyEvidence(t *testing.T) {
	curve, efficiency := workloadScaleEvidence(t, 0.5)
	efficiency.Reason = "tampered"
	if _, err := EvaluateWorkloadScaleOptions(curve, efficiency, []WorkloadScaleScenarioRequest{
		{TargetResourceFactor: 2, RequiredWorkload: 100, MinimumHeadroomPercent: 10},
	}); err == nil {
		t.Fatal("expected tampered efficiency evidence to be rejected")
	}
}
