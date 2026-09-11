package capacity

import "testing"

func TestEstimateWorkloadCurveRejectsTamperedEvidence(t *testing.T) {
	curve, err := BuildWorkloadCurve(
		WorkloadCurveRequest{ScopeID: "site-a", WorkloadUnit: WorkloadRequestsPerSecond, MinimumPoints: 3},
		workloadCurvePoints(),
	)
	if err != nil {
		t.Fatal(err)
	}
	curve.Points[1].SafeWorkload++
	if _, err := EstimateWorkloadCurve(curve, 2); err == nil {
		t.Fatal("expected tampered curve evidence to be rejected")
	}
}

func TestEstimateWorkloadCurveRejectsTamperedClassification(t *testing.T) {
	curve, err := BuildWorkloadCurve(
		WorkloadCurveRequest{ScopeID: "site-a", WorkloadUnit: WorkloadRequestsPerSecond, MinimumPoints: 3},
		workloadCurvePoints(),
	)
	if err != nil {
		t.Fatal(err)
	}
	curve.Confidence = WorkloadProfileConfidenceHigh
	if _, err := EstimateWorkloadCurve(curve, 2); err == nil {
		t.Fatal("expected tampered curve classification to be rejected")
	}
}
