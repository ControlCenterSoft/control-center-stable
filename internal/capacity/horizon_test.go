package capacity

import (
	"errors"
	"testing"
)

func horizonEvidence(current, growth, safe float64) (Forecast, Assessment) {
	forecast := Forecast{
		SchemaVersion:      ForecastSchemaV1,
		ForecastID:         "fc-test-001",
		ScopeID:            "site-a",
		WorkloadUnit:       WorkloadDevices,
		CurrentWorkload:    current,
		DailyGrowth:        growth,
		Quality:            ForecastQualityHigh,
		AdvisoryOnly:       true,
		ProductionMutation: false,
	}
	assessment := Assessment{
		SchemaVersion:      AssessmentSchemaV1,
		AssessmentID:       "cap-test-001",
		ScopeID:            "site-a",
		WorkloadUnit:       WorkloadDevices,
		SafeCapacity:       safe,
		Confidence:         Confidence{Level: ConfidenceHigh, Score: .85},
		Safe:               true,
		Action:             ActionNone,
		AdvisoryOnly:       true,
		ProductionMutation: false,
	}
	return forecast, assessment
}

func TestCapacityHorizonWarnsBeforeSafeCapacityExhaustion(t *testing.T) {
	forecast, assessment := horizonEvidence(70, 1, 100)
	request := CapacityHorizonRequest{ScopeID: "site-a", WorkloadUnit: WorkloadDevices, WarningHorizonDays: 40, CriticalHorizonDays: 10}

	horizon, err := BuildCapacityHorizon(request, forecast, assessment)
	if err != nil {
		t.Fatal(err)
	}
	if horizon.Risk != HorizonWarning || horizon.Action != ActionAdjustWorkloadPolicy || horizon.DaysToSafeCapacity == nil || *horizon.DaysToSafeCapacity != 30 {
		t.Fatalf("unexpected warning horizon: %#v", horizon)
	}
	if !horizon.AdvisoryOnly || horizon.ProductionMutation {
		t.Fatalf("capacity horizon crossed advisory boundary: %#v", horizon)
	}

	duplicate, err := BuildCapacityHorizon(request, forecast, assessment)
	if err != nil {
		t.Fatal(err)
	}
	if horizon.HorizonID != duplicate.HorizonID {
		t.Fatalf("horizon identity is not deterministic: %q %q", horizon.HorizonID, duplicate.HorizonID)
	}
}

func TestCapacityHorizonEscalatesCriticalAndUnsafeFleet(t *testing.T) {
	request := CapacityHorizonRequest{ScopeID: "site-a", WorkloadUnit: WorkloadDevices, WarningHorizonDays: 40, CriticalHorizonDays: 10}
	forecast, assessment := horizonEvidence(95, 1, 100)
	critical, err := BuildCapacityHorizon(request, forecast, assessment)
	if err != nil {
		t.Fatal(err)
	}
	if critical.Risk != HorizonCritical || critical.Action != ActionAddRoleCapacity || critical.DaysToSafeCapacity == nil || *critical.DaysToSafeCapacity != 5 {
		t.Fatalf("unexpected critical horizon: %#v", critical)
	}

	forecast, assessment = horizonEvidence(50, 0, 100)
	assessment.Safe = false
	unsafe, err := BuildCapacityHorizon(request, forecast, assessment)
	if err != nil {
		t.Fatal(err)
	}
	if unsafe.Risk != HorizonCritical || unsafe.Action != ActionAddRoleCapacity || unsafe.DaysToSafeCapacity != nil {
		t.Fatalf("unsafe fleet was not held critical: %#v", unsafe)
	}
}

func TestCapacityHorizonUsesUnknownForLowConfidence(t *testing.T) {
	forecast, assessment := horizonEvidence(70, 1, 100)
	forecast.Quality = ForecastQualityLow
	request := CapacityHorizonRequest{ScopeID: "site-a", WorkloadUnit: WorkloadDevices, WarningHorizonDays: 40, CriticalHorizonDays: 10}

	horizon, err := BuildCapacityHorizon(request, forecast, assessment)
	if err != nil {
		t.Fatal(err)
	}
	if horizon.Risk != HorizonUnknown || horizon.Action != ActionCollectEvidence || horizon.DaysToSafeCapacity != nil {
		t.Fatalf("low-confidence forecast was not held: %#v", horizon)
	}
}

func TestCapacityHorizonRejectsMismatchedEvidence(t *testing.T) {
	forecast, assessment := horizonEvidence(70, 1, 100)
	assessment.ScopeID = "site-b"
	request := CapacityHorizonRequest{ScopeID: "site-a", WorkloadUnit: WorkloadDevices, WarningHorizonDays: 40, CriticalHorizonDays: 10}

	_, err := BuildCapacityHorizon(request, forecast, assessment)
	if !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("expected invalid recommendation, got %v", err)
	}
}
