package capacity

import "testing"

func workloadScaleEvidence(t *testing.T, threshold float64) (WorkloadCurve, WorkloadCurveEfficiencyReport) {
	t.Helper()
	curve, err := BuildWorkloadCurve(
		WorkloadCurveRequest{ScopeID: "site-a", WorkloadUnit: WorkloadRequestsPerSecond, MinimumPoints: 3},
		workloadCurvePoints(),
	)
	if err != nil {
		t.Fatal(err)
	}
	efficiency, err := AnalyzeWorkloadCurveEfficiency(curve, WorkloadCurveEfficiencyPolicy{
		MinimumEfficiencyRatio: threshold,
		MinimumConfidence:      WorkloadProfileConfidenceMedium,
	})
	if err != nil {
		t.Fatal(err)
	}
	return curve, efficiency
}

func TestEvaluateWorkloadScaleScenarioFeasibleWithinObservedCurve(t *testing.T) {
	curve, efficiency := workloadScaleEvidence(t, 0.5)
	scenario, err := EvaluateWorkloadScaleScenario(curve, efficiency, WorkloadScaleScenarioRequest{
		TargetResourceFactor:   2,
		RequiredWorkload:       150,
		MinimumHeadroomPercent: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if scenario.Status != WorkloadScaleScenarioFeasible || scenario.RecommendedAction != "none" {
		t.Fatalf("unexpected scenario: %#v", scenario)
	}
	if scenario.EstimatedSafeWorkload == nil || !closeFloat(*scenario.EstimatedSafeWorkload, 180) {
		t.Fatalf("unexpected estimate: %#v", scenario)
	}
	if !scenario.AdvisoryOnly || scenario.ProductionMutation {
		t.Fatalf("unsafe scenario flags: %#v", scenario)
	}
}

func TestEvaluateWorkloadScaleScenarioReportsInsufficientHeadroom(t *testing.T) {
	curve, efficiency := workloadScaleEvidence(t, 0.5)
	scenario, err := EvaluateWorkloadScaleScenario(curve, efficiency, WorkloadScaleScenarioRequest{
		TargetResourceFactor:   2,
		RequiredWorkload:       175,
		MinimumHeadroomPercent: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if scenario.Status != WorkloadScaleScenarioInsufficient || scenario.Reason != "required_headroom_not_met" {
		t.Fatalf("expected insufficient scenario: %#v", scenario)
	}
}

func TestEvaluateWorkloadScaleScenarioPreservesDiminishingReturnsWarning(t *testing.T) {
	curve, efficiency := workloadScaleEvidence(t, 0.8)
	scenario, err := EvaluateWorkloadScaleScenario(curve, efficiency, WorkloadScaleScenarioRequest{
		TargetResourceFactor:   2,
		RequiredWorkload:       100,
		MinimumHeadroomPercent: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if scenario.Status != WorkloadScaleScenarioDiminishingReturns || scenario.RecommendedAction != "inspect-bottleneck-before-scaling" {
		t.Fatalf("expected diminishing returns warning: %#v", scenario)
	}
}

func TestEvaluateWorkloadScaleScenarioDoesNotExtrapolate(t *testing.T) {
	curve, efficiency := workloadScaleEvidence(t, 0.5)
	scenario, err := EvaluateWorkloadScaleScenario(curve, efficiency, WorkloadScaleScenarioRequest{
		TargetResourceFactor:   8,
		RequiredWorkload:       100,
		MinimumHeadroomPercent: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if scenario.Status != WorkloadScaleScenarioCollectEvidence || scenario.EstimatedSafeWorkload != nil {
		t.Fatalf("expected collect-evidence without extrapolation: %#v", scenario)
	}
}

func TestEvaluateWorkloadScaleScenarioRejectsTamperedEfficiencyEvidence(t *testing.T) {
	curve, efficiency := workloadScaleEvidence(t, 0.5)
	efficiency.Reason = "tampered"
	if _, err := EvaluateWorkloadScaleScenario(curve, efficiency, WorkloadScaleScenarioRequest{
		TargetResourceFactor:   2,
		RequiredWorkload:       100,
		MinimumHeadroomPercent: 10,
	}); err == nil {
		t.Fatal("expected tampered efficiency evidence to be rejected")
	}
}
