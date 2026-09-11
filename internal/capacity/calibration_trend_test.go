package capacity

import "testing"

func calibrationSnapshot(id string, day, multiplier float64, quality CalibrationQuality) CalibrationSnapshot {
	return CalibrationSnapshot{SnapshotID: id, Day: day, Calibration: CalibrationResult{
		SchemaVersion: CalibrationSchemaV1, CalibrationID: "cal-" + id,
		ScopeID: "site-a", WorkloadUnit: WorkloadDevices, SuggestedMultiplier: multiplier,
		P90AbsoluteErrorPercent: 5, Quality: quality, Status: CalibrationReady,
		AdjustmentAllowed: true, AdvisoryOnly: true, ProductionMutation: false,
	}}
}

func TestBuildCalibrationTrendStableAndDeterministic(t *testing.T) {
	req := CalibrationTrendRequest{ScopeID: "site-a", WorkloadUnit: WorkloadDevices, MinimumSnapshots: 3, MinimumQuality: CalibrationQualityMedium, MaxMultiplierDriftPer30DaysPct: 20}
	items := []CalibrationSnapshot{calibrationSnapshot("c", 60, 1.08, CalibrationQualityHigh), calibrationSnapshot("a", 0, 1.0, CalibrationQualityHigh), calibrationSnapshot("b", 30, 1.04, CalibrationQualityMedium)}
	got, err := BuildCalibrationTrend(req, items)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != CalibrationTrendStable {
		t.Fatalf("unexpected trend: %#v", got)
	}
	reversed := []CalibrationSnapshot{items[2], items[0], items[1]}
	got2, err := BuildCalibrationTrend(req, reversed)
	if err != nil {
		t.Fatal(err)
	}
	if got.TrendID != got2.TrendID {
		t.Fatalf("ids differ: %s %s", got.TrendID, got2.TrendID)
	}
}

func TestBuildCalibrationTrendDetectsDrift(t *testing.T) {
	req := CalibrationTrendRequest{ScopeID: "site-a", WorkloadUnit: WorkloadDevices, MinimumSnapshots: 3, MinimumQuality: CalibrationQualityLow, MaxMultiplierDriftPer30DaysPct: 10}
	items := []CalibrationSnapshot{calibrationSnapshot("a", 0, 1.0, CalibrationQualityHigh), calibrationSnapshot("b", 15, 1.1, CalibrationQualityHigh), calibrationSnapshot("c", 30, 1.25, CalibrationQualityHigh)}
	got, err := BuildCalibrationTrend(req, items)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != CalibrationTrendDrifting || got.Reason != "multiplier-drift-exceeds-policy" {
		t.Fatalf("unexpected trend: %#v", got)
	}
}

func TestBuildCalibrationTrendCollectsEvidence(t *testing.T) {
	req := CalibrationTrendRequest{ScopeID: "site-a", WorkloadUnit: WorkloadDevices, MinimumSnapshots: 3, MinimumQuality: CalibrationQualityMedium, MaxMultiplierDriftPer30DaysPct: 50}
	items := []CalibrationSnapshot{calibrationSnapshot("a", 0, 1, CalibrationQualityHigh), calibrationSnapshot("b", 30, 1.01, CalibrationQualityLow), calibrationSnapshot("c", 60, 1.02, CalibrationQualityHigh)}
	got, err := BuildCalibrationTrend(req, items)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != CalibrationTrendCollectEvidence {
		t.Fatalf("unexpected trend: %#v", got)
	}
}

func TestBuildCalibrationTrendRejectsScopeMismatch(t *testing.T) {
	req := CalibrationTrendRequest{ScopeID: "site-a", WorkloadUnit: WorkloadDevices, MinimumSnapshots: 3, MinimumQuality: CalibrationQualityLow, MaxMultiplierDriftPer30DaysPct: 50}
	items := []CalibrationSnapshot{calibrationSnapshot("a", 0, 1, CalibrationQualityHigh), calibrationSnapshot("b", 30, 1.01, CalibrationQualityHigh), calibrationSnapshot("c", 60, 1.02, CalibrationQualityHigh)}
	items[1].Calibration.ScopeID = "site-b"
	if _, err := BuildCalibrationTrend(req, items); err == nil {
		t.Fatal("expected scope mismatch")
	}
}
