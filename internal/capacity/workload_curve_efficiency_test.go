package capacity

import "testing"

func TestAnalyzeWorkloadCurveEfficiencyDetectsDiminishingReturns(t *testing.T) {
	curve, err := BuildWorkloadCurve(
		WorkloadCurveRequest{ScopeID: "site-a", WorkloadUnit: WorkloadRequestsPerSecond, MinimumPoints: 3},
		workloadCurvePoints(),
	)
	if err != nil {
		t.Fatal(err)
	}
	report, err := AnalyzeWorkloadCurveEfficiency(curve, WorkloadCurveEfficiencyPolicy{
		MinimumEfficiencyRatio: 0.8,
		MinimumConfidence:      WorkloadProfileConfidenceMedium,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != WorkloadCurveEfficiencyDiminishingReturns || report.RecommendedAction != "inspect-bottleneck-before-scaling" {
		t.Fatalf("unexpected report: %#v", report)
	}
	if len(report.Segments) != 2 || report.LowestEfficiency >= 0.8 {
		t.Fatalf("expected measurable diminishing returns: %#v", report)
	}
	if !report.AdvisoryOnly || report.ProductionMutation {
		t.Fatalf("unsafe flags: %#v", report)
	}
}

func TestAnalyzeWorkloadCurveEfficiencyAcceptsEfficientCurve(t *testing.T) {
	points := workloadCurvePoints()
	points[1].SafeWorkload = 200
	points[2].SafeWorkload = 400
	curve, err := BuildWorkloadCurve(
		WorkloadCurveRequest{ScopeID: "site-a", WorkloadUnit: WorkloadRequestsPerSecond, MinimumPoints: 3},
		points,
	)
	if err != nil {
		t.Fatal(err)
	}
	report, err := AnalyzeWorkloadCurveEfficiency(curve, WorkloadCurveEfficiencyPolicy{
		MinimumEfficiencyRatio: 0.9,
		MinimumConfidence:      WorkloadProfileConfidenceMedium,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != WorkloadCurveEfficiencyEfficient || report.RecommendedAction != "none" || !closeFloat(report.LowestEfficiency, 1) {
		t.Fatalf("unexpected efficient report: %#v", report)
	}
}

func TestAnalyzeWorkloadCurveEfficiencyRequiresConfidence(t *testing.T) {
	points := workloadCurvePoints()
	points[0].Confidence = WorkloadProfileConfidenceLow
	curve, err := BuildWorkloadCurve(
		WorkloadCurveRequest{ScopeID: "site-a", WorkloadUnit: WorkloadRequestsPerSecond, MinimumPoints: 3},
		points,
	)
	if err != nil {
		t.Fatal(err)
	}
	report, err := AnalyzeWorkloadCurveEfficiency(curve, WorkloadCurveEfficiencyPolicy{
		MinimumEfficiencyRatio: 0.5,
		MinimumConfidence:      WorkloadProfileConfidenceMedium,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != WorkloadCurveEfficiencyCollectEvidence || report.Reason != "curve_confidence_below_policy" {
		t.Fatalf("expected collect-evidence: %#v", report)
	}
}

func TestAnalyzeWorkloadCurveEfficiencyRejectsTamperedCurve(t *testing.T) {
	curve, err := BuildWorkloadCurve(
		WorkloadCurveRequest{ScopeID: "site-a", WorkloadUnit: WorkloadRequestsPerSecond, MinimumPoints: 3},
		workloadCurvePoints(),
	)
	if err != nil {
		t.Fatal(err)
	}
	curve.CurveID = "wlc-aaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := AnalyzeWorkloadCurveEfficiency(curve, WorkloadCurveEfficiencyPolicy{
		MinimumEfficiencyRatio: 0.5,
		MinimumConfidence:      WorkloadProfileConfidenceMedium,
	}); err == nil {
		t.Fatal("expected tampered curve to be rejected")
	}
}

func TestAnalyzeWorkloadCurveEfficiencyRejectsTruncatedReadyCurve(t *testing.T) {
	curve, err := BuildWorkloadCurve(
		WorkloadCurveRequest{ScopeID: "site-a", WorkloadUnit: WorkloadRequestsPerSecond, MinimumPoints: 3},
		workloadCurvePoints(),
	)
	if err != nil {
		t.Fatal(err)
	}
	curve.Points = curve.Points[:1]
	if _, err := AnalyzeWorkloadCurveEfficiency(curve, WorkloadCurveEfficiencyPolicy{
		MinimumEfficiencyRatio: 0.5,
		MinimumConfidence:      WorkloadProfileConfidenceMedium,
	}); err == nil {
		t.Fatal("expected truncated ready curve to be rejected")
	}
}

func TestAnalyzeWorkloadCurveEfficiencyRejectsDuplicateResourceFactorEvidence(t *testing.T) {
	curve, err := BuildWorkloadCurve(
		WorkloadCurveRequest{ScopeID: "site-a", WorkloadUnit: WorkloadRequestsPerSecond, MinimumPoints: 3},
		workloadCurvePoints(),
	)
	if err != nil {
		t.Fatal(err)
	}
	curve.Points[1].ResourceFactor = curve.Points[0].ResourceFactor
	if _, err := AnalyzeWorkloadCurveEfficiency(curve, WorkloadCurveEfficiencyPolicy{
		MinimumEfficiencyRatio: 0.5,
		MinimumConfidence:      WorkloadProfileConfidenceMedium,
	}); err == nil {
		t.Fatal("expected non-increasing resource-factor evidence to be rejected")
	}
}

func TestAnalyzeWorkloadCurveEfficiencyIsDeterministic(t *testing.T) {
	curve, err := BuildWorkloadCurve(
		WorkloadCurveRequest{ScopeID: "site-a", WorkloadUnit: WorkloadRequestsPerSecond, MinimumPoints: 3},
		workloadCurvePoints(),
	)
	if err != nil {
		t.Fatal(err)
	}
	policy := WorkloadCurveEfficiencyPolicy{MinimumEfficiencyRatio: 0.7, MinimumConfidence: WorkloadProfileConfidenceMedium}
	first, err := AnalyzeWorkloadCurveEfficiency(curve, policy)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AnalyzeWorkloadCurveEfficiency(curve, policy)
	if err != nil {
		t.Fatal(err)
	}
	if first.ReportID != second.ReportID {
		t.Fatalf("report IDs differ: %q != %q", first.ReportID, second.ReportID)
	}
}
