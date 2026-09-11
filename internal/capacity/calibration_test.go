package capacity

import "testing"

func TestBuildCalibrationDeterministicReady(t *testing.T) {
	req := CalibrationRequest{ScopeID: "site-a", WorkloadUnit: WorkloadDevices, MinimumSamples: 3, MaxAdjustmentPercent: 20, MaxP90ErrorPercent: 20}
	observations := []CalibrationObservation{
		{ObservationID: "c", PredictedWorkload: 100, ObservedWorkload: 110},
		{ObservationID: "a", PredictedWorkload: 200, ObservedWorkload: 220},
		{ObservationID: "b", PredictedWorkload: 50, ObservedWorkload: 55},
	}
	got, err := BuildCalibration(req, observations)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != CalibrationReady || !got.AdjustmentAllowed {
		t.Fatalf("unexpected status: %#v", got)
	}
	if !closeFloat(got.SuggestedMultiplier, 1.1) {
		t.Fatalf("multiplier=%v", got.SuggestedMultiplier)
	}
	reversed := []CalibrationObservation{observations[2], observations[0], observations[1]}
	got2, err := BuildCalibration(req, reversed)
	if err != nil {
		t.Fatal(err)
	}
	if got.CalibrationID != got2.CalibrationID {
		t.Fatalf("ids differ: %s %s", got.CalibrationID, got2.CalibrationID)
	}
}

func TestBuildCalibrationBlocksLargeAdjustment(t *testing.T) {
	req := CalibrationRequest{ScopeID: "site-a", WorkloadUnit: WorkloadInstances, MinimumSamples: 3, MaxAdjustmentPercent: 10, MaxP90ErrorPercent: 100}
	observations := []CalibrationObservation{{"a", 100, 140}, {"b", 100, 145}, {"c", 100, 150}}
	got, err := BuildCalibration(req, observations)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != CalibrationBlocked || got.AdjustmentAllowed || !closeFloat(got.SuggestedMultiplier, 1) {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestBuildCalibrationCollectsEvidenceForNoisyModel(t *testing.T) {
	req := CalibrationRequest{ScopeID: "site-a", WorkloadUnit: WorkloadJobsPerMinute, MinimumSamples: 3, MaxAdjustmentPercent: 50, MaxP90ErrorPercent: 15}
	observations := []CalibrationObservation{{"a", 100, 105}, {"b", 100, 90}, {"c", 100, 130}}
	got, err := BuildCalibration(req, observations)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != CalibrationCollectEvidence || got.AdjustmentAllowed {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestBuildCalibrationRejectsDuplicateEvidence(t *testing.T) {
	req := CalibrationRequest{ScopeID: "site-a", WorkloadUnit: WorkloadDevices, MinimumSamples: 3, MaxAdjustmentPercent: 20, MaxP90ErrorPercent: 20}
	_, err := BuildCalibration(req, []CalibrationObservation{{"a", 1, 1}, {"a", 2, 2}, {"b", 3, 3}})
	if err == nil {
		t.Fatal("expected duplicate observation error")
	}
}
