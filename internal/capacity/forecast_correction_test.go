package capacity

import "testing"

func testForecast() Forecast {
	return Forecast{
		SchemaVersion:      ForecastSchemaV1,
		ForecastID:         "fc-aaaaaaaaaaaaaaaaaaaaaaaa",
		ScopeID:            "site-a",
		WorkloadUnit:       WorkloadDevices,
		ProjectedWorkload:  100,
		PlanningWorkload:   120,
		AdvisoryOnly:       true,
		ProductionMutation: false,
	}
}

func testCalibration(multiplier float64, quality CalibrationQuality) CalibrationResult {
	return CalibrationResult{
		SchemaVersion:       CalibrationSchemaV1,
		CalibrationID:       "cal-bbbbbbbbbbbbbbbbbbbbbbbb",
		ScopeID:             "site-a",
		WorkloadUnit:        WorkloadDevices,
		SuggestedMultiplier: multiplier,
		Quality:             quality,
		Status:              CalibrationReady,
		AdjustmentAllowed:   true,
		AdvisoryOnly:        true,
		ProductionMutation:  false,
	}
}

func TestBuildForecastCorrectionIncreasesConservativePlanningLoad(t *testing.T) {
	request := ForecastCorrectionRequest{Forecast: testForecast(), Calibration: testCalibration(1.1, CalibrationQualityHigh), MinimumQuality: CalibrationQualityMedium}
	got, err := BuildForecastCorrection(request)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != ForecastCorrectionReady || got.Reason != "calibration-ready" {
		t.Fatalf("unexpected result: %#v", got)
	}
	if !closeFloat(got.CorrectedProjectedWorkload, 110) || !closeFloat(got.CorrectedPlanningWorkload, 132) {
		t.Fatalf("unexpected workloads: %#v", got)
	}
	got2, err := BuildForecastCorrection(request)
	if err != nil {
		t.Fatal(err)
	}
	if got.CorrectionID != got2.CorrectionID {
		t.Fatalf("ids differ: %s %s", got.CorrectionID, got2.CorrectionID)
	}
}

func TestBuildForecastCorrectionNeverReducesPlanningLoad(t *testing.T) {
	got, err := BuildForecastCorrection(ForecastCorrectionRequest{Forecast: testForecast(), Calibration: testCalibration(.9, CalibrationQualityHigh), MinimumQuality: CalibrationQualityLow})
	if err != nil {
		t.Fatal(err)
	}
	if !got.ConservativeFloorApplied || got.Reason != "conservative-floor" || !closeFloat(got.EffectiveMultiplier, 1) {
		t.Fatalf("unexpected result: %#v", got)
	}
	if !closeFloat(got.CorrectedPlanningWorkload, got.OriginalPlanningWorkload) {
		t.Fatalf("planning load decreased: %#v", got)
	}
}

func TestBuildForecastCorrectionBlocksInsufficientQuality(t *testing.T) {
	got, err := BuildForecastCorrection(ForecastCorrectionRequest{Forecast: testForecast(), Calibration: testCalibration(1.1, CalibrationQualityLow), MinimumQuality: CalibrationQualityMedium})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != ForecastCorrectionBlocked || got.Reason != "calibration-quality-below-policy" || !closeFloat(got.EffectiveMultiplier, 1) {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestBuildForecastCorrectionRejectsScopeMismatch(t *testing.T) {
	calibration := testCalibration(1.1, CalibrationQualityHigh)
	calibration.ScopeID = "site-b"
	_, err := BuildForecastCorrection(ForecastCorrectionRequest{Forecast: testForecast(), Calibration: calibration, MinimumQuality: CalibrationQualityLow})
	if err == nil {
		t.Fatal("expected scope mismatch")
	}
}
