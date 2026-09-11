package capacity

import "testing"

func workloadProfileRequest() WorkloadProfileRequest {
	return WorkloadProfileRequest{
		ScopeID:                   "site-a",
		WorkloadUnit:              WorkloadRequestsPerSecond,
		MinimumSamples:            4,
		TargetUtilizationPercent:  75,
		MaxErrorRatePercent:       1,
		MaxP95LatencyMilliseconds: 250,
		SafetyReservePercent:      20,
	}
}

func workloadProfileSamples() []WorkloadProfileSample {
	return []WorkloadProfileSample{
		{SampleID: "s1", Workload: 100, UtilizationPercent: 35, ErrorRatePercent: 0, P95LatencyMilliseconds: 70},
		{SampleID: "s2", Workload: 200, UtilizationPercent: 55, ErrorRatePercent: 0.2, P95LatencyMilliseconds: 110},
		{SampleID: "s3", Workload: 300, UtilizationPercent: 72, ErrorRatePercent: 0.5, P95LatencyMilliseconds: 180},
		{SampleID: "s4", Workload: 400, UtilizationPercent: 89, ErrorRatePercent: 2.5, P95LatencyMilliseconds: 410},
	}
}

func TestBuildWorkloadProfileReadyAndDeterministic(t *testing.T) {
	request := workloadProfileRequest()
	first, err := BuildWorkloadProfile(request, workloadProfileSamples())
	if err != nil {
		t.Fatal(err)
	}
	reversed := workloadProfileSamples()
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	second, err := BuildWorkloadProfile(request, reversed)
	if err != nil {
		t.Fatal(err)
	}
	if first.ProfileID != second.ProfileID {
		t.Fatalf("profile IDs differ: %q != %q", first.ProfileID, second.ProfileID)
	}
	if first.Status != WorkloadProfileReady || first.Reason != "slo_boundary_observed" {
		t.Fatalf("unexpected status: %#v", first)
	}
	if first.ObservedSafeBoundary != 300 || first.RecommendedSafeWorkload != 240 || first.ObservedTechnicalBoundary != 400 {
		t.Fatalf("unexpected boundaries: %#v", first)
	}
	if !first.BoundaryObserved || !first.AdvisoryOnly || first.ProductionMutation {
		t.Fatalf("unsafe or incomplete result: %#v", first)
	}
}

func TestBuildWorkloadProfileCollectsEvidenceWithoutFailureBoundary(t *testing.T) {
	samples := workloadProfileSamples()
	samples[3] = WorkloadProfileSample{SampleID: "s4", Workload: 400, UtilizationPercent: 74, ErrorRatePercent: 0.5, P95LatencyMilliseconds: 200}
	result, err := BuildWorkloadProfile(workloadProfileRequest(), samples)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != WorkloadProfileCollectEvidence || result.BoundaryObserved {
		t.Fatalf("expected collect-evidence without boundary: %#v", result)
	}
	if result.ObservedSafeBoundary != 400 || result.ObservedTechnicalBoundary != 400 || result.RecommendedSafeWorkload != 320 {
		t.Fatalf("unexpected lower-bound profile: %#v", result)
	}
}

func TestBuildWorkloadProfileBlocksNonMonotonicEvidence(t *testing.T) {
	samples := workloadProfileSamples()
	samples[1].ErrorRatePercent = 5
	samples[2].ErrorRatePercent = 0.1
	result, err := BuildWorkloadProfile(workloadProfileRequest(), samples)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != WorkloadProfileBlocked || result.Reason != "non_monotonic_slo_evidence" {
		t.Fatalf("expected fail-closed non-monotonic result: %#v", result)
	}
}

func TestBuildWorkloadProfileBlocksWhenNoSampleMeetsSLO(t *testing.T) {
	samples := workloadProfileSamples()
	for index := range samples {
		samples[index].UtilizationPercent = 99
	}
	result, err := BuildWorkloadProfile(workloadProfileRequest(), samples)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != WorkloadProfileBlocked || result.Reason != "no_slo_conforming_sample" || result.RecommendedSafeWorkload != 0 {
		t.Fatalf("expected blocked profile: %#v", result)
	}
}

func TestBuildWorkloadProfileRejectsDuplicateWorkload(t *testing.T) {
	samples := workloadProfileSamples()
	samples[3].Workload = samples[2].Workload
	if _, err := BuildWorkloadProfile(workloadProfileRequest(), samples); err == nil {
		t.Fatal("expected duplicate workload to be rejected")
	}
}
