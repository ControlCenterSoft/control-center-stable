package capacity

import (
	"errors"
	"testing"
)

func TestForecastProjectsTelemetryGrowthDeterministically(t *testing.T) {
	request := ForecastRequest{ScopeID: "site-a", WorkloadUnit: WorkloadDevices, HorizonDays: 3, SafetyMarginPercent: 20}
	observations := []GrowthObservation{
		{ObservationID: "sample-7", Day: 7, Workload: 70},
		{ObservationID: "sample-0", Day: 0, Workload: 0},
		{ObservationID: "sample-3", Day: 3, Workload: 30},
		{ObservationID: "sample-1", Day: 1, Workload: 10},
		{ObservationID: "sample-6", Day: 6, Workload: 60},
		{ObservationID: "sample-2", Day: 2, Workload: 20},
		{ObservationID: "sample-5", Day: 5, Workload: 50},
		{ObservationID: "sample-4", Day: 4, Workload: 40},
	}
	forecast, err := BuildForecast(request, observations)
	if err != nil {
		t.Fatal(err)
	}
	if forecast.SchemaVersion != ForecastSchemaV1 || forecast.SampleCount != 8 {
		t.Fatalf("unexpected forecast identity: %#v", forecast)
	}
	if !closeFloat(forecast.DailyGrowth, 10) || !closeFloat(forecast.CurrentWorkload, 70) || !closeFloat(forecast.ProjectedWorkload, 100) || !closeFloat(forecast.PlanningWorkload, 120) {
		t.Fatalf("unexpected forecast values: %#v", forecast)
	}
	if forecast.Quality != ForecastQualityHigh || !closeFloat(forecast.FitRSquared, 1) {
		t.Fatalf("unexpected forecast quality: %#v", forecast)
	}
	if !forecast.AdvisoryOnly || forecast.ProductionMutation {
		t.Fatalf("forecast crossed advisory boundary: %#v", forecast)
	}

	reversed := append([]GrowthObservation(nil), observations...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	other, err := BuildForecast(request, reversed)
	if err != nil {
		t.Fatal(err)
	}
	if forecast != other {
		t.Fatalf("forecast depends on input order: %#v %#v", forecast, other)
	}
}

func TestForecastRejectsDuplicateObservationDays(t *testing.T) {
	request := ForecastRequest{ScopeID: "site-a", WorkloadUnit: WorkloadDevices, HorizonDays: 30, SafetyMarginPercent: 10}
	_, err := BuildForecast(request, []GrowthObservation{
		{ObservationID: "sample-a", Day: 1, Workload: 10},
		{ObservationID: "sample-b", Day: 1, Workload: 20},
	})
	if !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestForecastClampsNegativeProjectionAtZero(t *testing.T) {
	request := ForecastRequest{ScopeID: "site-a", WorkloadUnit: WorkloadDevices, HorizonDays: 10, SafetyMarginPercent: 25}
	forecast, err := BuildForecast(request, []GrowthObservation{
		{ObservationID: "sample-a", Day: 0, Workload: 20},
		{ObservationID: "sample-b", Day: 1, Workload: 10},
		{ObservationID: "sample-c", Day: 2, Workload: 0},
		{ObservationID: "sample-d", Day: 3, Workload: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if forecast.ProjectedWorkload != 0 || forecast.PlanningWorkload != 0 {
		t.Fatalf("negative projection was not clamped: %#v", forecast)
	}
}
