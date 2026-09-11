package capacity

import "testing"

func workloadCurvePoints() []WorkloadCurvePoint {
	return []WorkloadCurvePoint{
		{PointID: "point-01", SourceProfileID: "wlp-111111111111111111111111", ResourceFactor: 1, SafeWorkload: 100, Confidence: WorkloadProfileConfidenceHigh},
		{PointID: "point-02", SourceProfileID: "wlp-222222222222222222222222", ResourceFactor: 2, SafeWorkload: 180, Confidence: WorkloadProfileConfidenceMedium},
		{PointID: "point-03", SourceProfileID: "wlp-333333333333333333333333", ResourceFactor: 4, SafeWorkload: 300, Confidence: WorkloadProfileConfidenceHigh},
	}
}

func TestBuildWorkloadCurveDeterministicAndConservativeConfidence(t *testing.T) {
	request := WorkloadCurveRequest{ScopeID: "site-a", WorkloadUnit: WorkloadRequestsPerSecond, MinimumPoints: 3}
	first, err := BuildWorkloadCurve(request, workloadCurvePoints())
	if err != nil {
		t.Fatal(err)
	}
	reversed := workloadCurvePoints()
	reversed[0], reversed[2] = reversed[2], reversed[0]
	second, err := BuildWorkloadCurve(request, reversed)
	if err != nil {
		t.Fatal(err)
	}
	if first.CurveID != second.CurveID {
		t.Fatalf("curve IDs differ: %q != %q", first.CurveID, second.CurveID)
	}
	if first.Status != WorkloadCurveReady || first.Confidence != WorkloadProfileConfidenceMedium {
		t.Fatalf("unexpected curve: %#v", first)
	}
	if !first.AdvisoryOnly || first.ProductionMutation {
		t.Fatalf("unsafe curve flags: %#v", first)
	}
}

func TestEstimateWorkloadCurveInterpolatesWithinObservedEvidence(t *testing.T) {
	curve, err := BuildWorkloadCurve(
		WorkloadCurveRequest{ScopeID: "site-a", WorkloadUnit: WorkloadRequestsPerSecond, MinimumPoints: 3},
		workloadCurvePoints(),
	)
	if err != nil {
		t.Fatal(err)
	}
	estimate, err := EstimateWorkloadCurve(curve, 3)
	if err != nil {
		t.Fatal(err)
	}
	if estimate.Status != WorkloadCurveReady || estimate.Reason != "interpolated_within_observed_curve" || estimate.EstimatedSafeWorkload == nil {
		t.Fatalf("unexpected estimate: %#v", estimate)
	}
	if !closeFloat(*estimate.EstimatedSafeWorkload, 240) {
		t.Fatalf("unexpected interpolated workload: %v", *estimate.EstimatedSafeWorkload)
	}
	if !estimate.AdvisoryOnly || estimate.ProductionMutation {
		t.Fatalf("unsafe estimate flags: %#v", estimate)
	}
}

func TestEstimateWorkloadCurveDoesNotExtrapolate(t *testing.T) {
	curve, err := BuildWorkloadCurve(
		WorkloadCurveRequest{ScopeID: "site-a", WorkloadUnit: WorkloadRequestsPerSecond, MinimumPoints: 3},
		workloadCurvePoints(),
	)
	if err != nil {
		t.Fatal(err)
	}
	estimate, err := EstimateWorkloadCurve(curve, 8)
	if err != nil {
		t.Fatal(err)
	}
	if estimate.Status != WorkloadCurveCollectEvidence || estimate.Reason != "target_outside_observed_curve" || estimate.EstimatedSafeWorkload != nil {
		t.Fatalf("expected collect-evidence without extrapolation: %#v", estimate)
	}
}

func TestBuildWorkloadCurveBlocksContradictoryCapacityEvidence(t *testing.T) {
	points := workloadCurvePoints()
	points[2].SafeWorkload = 150
	curve, err := BuildWorkloadCurve(
		WorkloadCurveRequest{ScopeID: "site-a", WorkloadUnit: WorkloadRequestsPerSecond, MinimumPoints: 3},
		points,
	)
	if err != nil {
		t.Fatal(err)
	}
	if curve.Status != WorkloadCurveBlocked || curve.Reason != "non_monotonic_capacity_curve" {
		t.Fatalf("expected blocked contradictory curve: %#v", curve)
	}
	estimate, err := EstimateWorkloadCurve(curve, 2)
	if err != nil {
		t.Fatal(err)
	}
	if estimate.Status != WorkloadCurveBlocked || estimate.EstimatedSafeWorkload != nil {
		t.Fatalf("blocked curve must not estimate capacity: %#v", estimate)
	}
}

func TestBuildWorkloadCurveRejectsDuplicateResourceFactor(t *testing.T) {
	points := workloadCurvePoints()
	points[2].ResourceFactor = points[1].ResourceFactor
	if _, err := BuildWorkloadCurve(
		WorkloadCurveRequest{ScopeID: "site-a", WorkloadUnit: WorkloadRequestsPerSecond, MinimumPoints: 3},
		points,
	); err == nil {
		t.Fatal("expected duplicate resource factor to be rejected")
	}
}
