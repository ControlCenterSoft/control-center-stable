package capacity

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"control-center/internal/agent"
	"control-center/internal/corecontracts"
)

var contractNow = time.Date(2026, 9, 8, 12, 30, 0, 0, time.UTC)

func validMetadata(id, resourceVersion string) corecontracts.ObjectMetadata {
	return corecontracts.ObjectMetadata{
		ObjectID: id, ScopeID: "site-a", OwnerScope: "site-a", Generation: 1,
		ResourceVersion: resourceVersion,
		CreatedAt:       contractNow.Add(-24 * time.Hour),
		UpdatedAt:       contractNow,
	}
}

func validEvidence(id string, metric agent.CapacityMetric, target string, value float64, unit agent.CapacityUnit, observedAt time.Time, kind agent.ObservationEvidence, samples uint64) Evidence {
	return Evidence{
		ID: id, SampleCount: samples,
		Observation: agent.CapacityObservation{
			Metric: metric, TargetID: target, Value: value, Unit: unit,
			ObservedAt: observedAt, Evidence: kind,
		},
	}
}

func validProfile() Profile {
	return Profile{
		ObjectMetadata:        validMetadata("profile-001", "rv-profile-1"),
		SchemaVersion:         ProfileSchemaV1,
		Subject:               Subject{Kind: SubjectNode, ID: "node-001"},
		WorkloadUnit:          WorkloadDevices,
		SafeCapacity:          1000,
		TechnicalLimit:        1250,
		MinimumReservePercent: 20,
		Constraints: []Constraint{
			{
				SchemaVersion: ConstraintSchemaV1, ID: "storage-pressure", Metric: agent.MetricStorageUsed,
				TargetID: "disk-001", Unit: agent.UnitBytes, SafeLimit: 700, TechnicalLimit: 900,
				MaxObservationAgeSeconds: 3600, MinimumSamples: 20,
				AcceptedEvidence: []agent.ObservationEvidence{agent.EvidenceMeasured, agent.EvidenceBenchmark},
			},
			{
				SchemaVersion: ConstraintSchemaV1, ID: "cpu-pressure", Metric: agent.MetricCPUUtilization,
				TargetID: "node-001", Unit: agent.UnitPercent, SafeLimit: 70, TechnicalLimit: 95,
				MaxObservationAgeSeconds: 3600, MinimumSamples: 10,
				AcceptedEvidence: []agent.ObservationEvidence{agent.EvidenceMeasured},
			},
		},
		Confidence: Confidence{Level: ConfidenceHigh, Score: 0.85},
		Evidence: []Evidence{
			validEvidence("storage-profile", agent.MetricStorageUsed, "disk-001", 600, agent.UnitBytes, contractNow.Add(-45*time.Minute), agent.EvidenceBenchmark, 70),
			validEvidence("cpu-profile", agent.MetricCPUUtilization, "node-001", 50, agent.UnitPercent, contractNow.Add(-50*time.Minute), agent.EvidenceMeasured, 30),
		},
		CalibratedAt: contractNow.Add(-30 * time.Minute),
	}
}

func validRecommendationEvidence() []Evidence {
	return []Evidence{
		validEvidence("storage-current", agent.MetricStorageUsed, "disk-001", 720, agent.UnitBytes, contractNow.Add(-5*time.Minute), agent.EvidenceMeasured, 30),
		validEvidence("cpu-current", agent.MetricCPUUtilization, "node-001", 63, agent.UnitPercent, contractNow.Add(-4*time.Minute), agent.EvidenceMeasured, 20),
	}
}

func validRecommendationDraft(profile Profile) RecommendationDraft {
	generation := profile.Generation
	return RecommendationDraft{
		ObjectMetadata: validMetadata("recommendation-001", "rv-recommendation-1"),
		ProfilePrecondition: corecontracts.ObjectPrecondition{
			ObjectID: profile.ObjectID, ResourceVersion: profile.ResourceVersion, Generation: &generation,
		},
		EvaluatedAt: contractNow, CurrentWorkload: 1050,
		Confidence:   Confidence{Level: ConfidenceHigh, Score: 0.8},
		Evidence:     validRecommendationEvidence(),
		Action:       ActionIncreaseStorage,
		Summary:      "Превышена безопасная граница хранения; требуется добавить резерв.",
		AdvisoryOnly: true,
	}
}

func TestNormalizeProfileCanonicalizesBoundedContract(t *testing.T) {
	profile := validProfile()
	profile.Subject.Kind = " NODE "
	profile.WorkloadUnit = " DEVICES "
	profile.Constraints[0].Metric = " STORAGE.USED "
	profile.Constraints[0].Unit = " BYTES "
	profile.Constraints[0].AcceptedEvidence = []agent.ObservationEvidence{agent.EvidenceBenchmark, agent.EvidenceMeasured}
	profile.Evidence[0].Observation.TargetID = "disk-001"

	got, err := NormalizeProfile(profile)
	if err != nil {
		t.Fatalf("NormalizeProfile() error = %v", err)
	}
	if got.Subject.Kind != SubjectNode || got.WorkloadUnit != WorkloadDevices {
		t.Fatalf("canonical subject/workload = %#v/%q", got.Subject, got.WorkloadUnit)
	}
	if got.Constraints[0].ID != "cpu-pressure" || got.Evidence[0].ID != "cpu-profile" {
		t.Fatalf("collections are not deterministic: constraints=%#v evidence=%#v", got.Constraints, got.Evidence)
	}
	if got.Constraints[1].AcceptedEvidence[0] != agent.EvidenceBenchmark {
		t.Fatalf("accepted evidence = %#v", got.Constraints[1].AcceptedEvidence)
	}
	if got.CalibratedAt.Location() != time.UTC {
		t.Fatalf("calibrated_at location = %v", got.CalibratedAt.Location())
	}
}

func TestNormalizeProfileRejectsUnknownSchemaAndUnsafeValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Profile)
		want   error
	}{
		{name: "missing schema", mutate: func(value *Profile) { value.SchemaVersion = "" }, want: ErrUnsupportedSchema},
		{name: "future schema", mutate: func(value *Profile) { value.SchemaVersion = "capacity.profile/v2" }, want: ErrUnsupportedSchema},
		{name: "unknown subject", mutate: func(value *Profile) { value.Subject.Kind = "cluster" }, want: ErrInvalidProfile},
		{name: "role on node subject", mutate: func(value *Profile) { value.Subject.Role = corecontracts.RoleWorkerNode }, want: ErrInvalidProfile},
		{name: "unknown workload", mutate: func(value *Profile) { value.WorkloadUnit = "users" }, want: ErrInvalidProfile},
		{name: "safe equals technical", mutate: func(value *Profile) { value.SafeCapacity = value.TechnicalLimit }, want: ErrInvalidProfile},
		{name: "nan capacity", mutate: func(value *Profile) { value.SafeCapacity = math.NaN() }, want: ErrInvalidProfile},
		{name: "reserve not preserved", mutate: func(value *Profile) { value.MinimumReservePercent = 21 }, want: ErrInvalidProfile},
		{name: "future calibration", mutate: func(value *Profile) { value.CalibratedAt = value.UpdatedAt.Add(time.Second) }, want: ErrInvalidProfile},
		{name: "no constraints", mutate: func(value *Profile) { value.Constraints = nil }, want: ErrInvalidProfile},
		{name: "duplicate constraint id", mutate: func(value *Profile) { value.Constraints[1].ID = value.Constraints[0].ID }, want: ErrInvalidProfile},
		{name: "duplicate metric target", mutate: func(value *Profile) {
			value.Constraints[1].Metric = value.Constraints[0].Metric
			value.Constraints[1].Unit = value.Constraints[0].Unit
			value.Constraints[1].TargetID = value.Constraints[0].TargetID
			value.Constraints[1].SafeLimit = 650
			value.Constraints[1].TechnicalLimit = 850
		}, want: ErrInvalidProfile},
		{name: "metric unit drift", mutate: func(value *Profile) { value.Constraints[0].Unit = agent.UnitPercent }, want: ErrInvalidProfile},
		{name: "metric maximum", mutate: func(value *Profile) { value.Constraints[1].TechnicalLimit = 101 }, want: ErrInvalidProfile},
		{name: "zero freshness", mutate: func(value *Profile) { value.Constraints[0].MaxObservationAgeSeconds = 0 }, want: ErrInvalidProfile},
		{name: "zero samples", mutate: func(value *Profile) { value.Constraints[0].MinimumSamples = 0 }, want: ErrInvalidProfile},
		{name: "duplicate evidence id", mutate: func(value *Profile) { value.Evidence[1].ID = value.Evidence[0].ID }, want: ErrInvalidProfile},
		{name: "evidence after calibration", mutate: func(value *Profile) { value.Evidence[0].Observation.ObservedAt = value.CalibratedAt.Add(time.Second) }, want: ErrInvalidProfile},
		{name: "stale calibration evidence", mutate: func(value *Profile) {
			value.Evidence[0].Observation.ObservedAt = value.CalibratedAt.Add(-2 * time.Hour)
		}, want: ErrInvalidProfile},
		{name: "insufficient samples", mutate: func(value *Profile) { value.Evidence[0].SampleCount = 1 }, want: ErrInvalidProfile},
		{name: "confidence band", mutate: func(value *Profile) { value.Confidence.Score = 0.5 }, want: ErrInvalidProfile},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := validProfile()
			test.mutate(&profile)
			_, err := NormalizeProfile(profile)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestNormalizeProfileCertifiedConfidenceRequiresMeasuredBenchmarkAndSamples(t *testing.T) {
	profile := validProfile()
	profile.Confidence = Confidence{Level: ConfidenceCertified, Score: 0.96}
	if _, err := NormalizeProfile(profile); err != nil {
		t.Fatalf("certified profile rejected: %v", err)
	}
	profile.Evidence[0].Observation.Evidence = agent.EvidenceMeasured
	if _, err := NormalizeProfile(profile); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("error = %v, want invalid profile", err)
	}
}

func TestNormalizeRoleProfileUsesCanonicalCoreRole(t *testing.T) {
	profile := validProfile()
	profile.Subject = Subject{Kind: SubjectRole, ID: "worker-pool-a", Role: " WORKER-NODE "}
	got, err := NormalizeProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject.Role != corecontracts.RoleWorkerNode {
		t.Fatalf("role = %q", got.Subject.Role)
	}
	profile.Subject.Role = "regional-master"
	if _, err := NormalizeProfile(profile); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("error = %v, want invalid profile", err)
	}
}

func TestValidateProfileSuccessorEnforcesGeneration(t *testing.T) {
	current := validProfile()
	next := validProfile()
	next.ResourceVersion = "rv-profile-2"
	next.UpdatedAt = current.UpdatedAt.Add(time.Minute)
	next.SafeCapacity = 950
	next.Generation = 2
	if err := ValidateProfileSuccessor(current, next); err != nil {
		t.Fatalf("ValidateProfileSuccessor() error = %v", err)
	}
	next.Generation = 1
	if err := ValidateProfileSuccessor(current, next); err == nil {
		t.Fatal("changed profile accepted without generation increment")
	}
}

func TestNormalizeConstraintRejectsImplicitLegacyMigration(t *testing.T) {
	constraint := validProfile().Constraints[0]
	constraint.SchemaVersion = ""
	_, err := NormalizeConstraint(constraint)
	if !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("error = %v, want unsupported schema", err)
	}
}

func TestNormalizeConstraintRejectsEveryUnsafeBoundary(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Constraint)
	}{
		{name: "invalid id", mutate: func(value *Constraint) { value.ID = "-constraint" }},
		{name: "invalid target", mutate: func(value *Constraint) { value.TargetID = "disk 001" }},
		{name: "unknown metric", mutate: func(value *Constraint) { value.Metric = "storage.custom" }},
		{name: "wrong unit", mutate: func(value *Constraint) { value.Unit = agent.UnitPercent }},
		{name: "zero safe limit", mutate: func(value *Constraint) { value.SafeLimit = 0 }},
		{name: "non-finite safe limit", mutate: func(value *Constraint) { value.SafeLimit = math.Inf(1) }},
		{name: "equal limits", mutate: func(value *Constraint) { value.SafeLimit = value.TechnicalLimit }},
		{name: "non-finite technical limit", mutate: func(value *Constraint) { value.TechnicalLimit = math.NaN() }},
		{name: "freshness too large", mutate: func(value *Constraint) { value.MaxObservationAgeSeconds = 604801 }},
		{name: "sample bound too large", mutate: func(value *Constraint) { value.MinimumSamples = 1_000_000_000_001 }},
		{name: "missing accepted evidence", mutate: func(value *Constraint) { value.AcceptedEvidence = nil }},
		{name: "unknown accepted evidence", mutate: func(value *Constraint) {
			value.AcceptedEvidence = []agent.ObservationEvidence{"synthetic"}
		}},
		{name: "duplicate accepted evidence", mutate: func(value *Constraint) {
			value.AcceptedEvidence = []agent.ObservationEvidence{agent.EvidenceMeasured, agent.EvidenceMeasured}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			constraint := validProfile().Constraints[0]
			test.mutate(&constraint)
			if _, err := NormalizeConstraint(constraint); !errors.Is(err, ErrInvalidConstraint) {
				t.Fatalf("error = %v, want invalid constraint", err)
			}
		})
	}
}

func TestNormalizeProfileRejectsInvalidEvidenceEnvelope(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Profile)
	}{
		{name: "zero sample count", mutate: func(value *Profile) { value.Evidence[0].SampleCount = 0 }},
		{name: "invalid target", mutate: func(value *Profile) { value.Evidence[0].Observation.TargetID = "disk 001" }},
		{name: "unknown metric", mutate: func(value *Profile) { value.Evidence[0].Observation.Metric = "storage.custom" }},
		{name: "wrong unit", mutate: func(value *Profile) { value.Evidence[0].Observation.Unit = agent.UnitPercent }},
		{name: "non-finite observation", mutate: func(value *Profile) { value.Evidence[0].Observation.Value = math.NaN() }},
		{name: "missing observation time", mutate: func(value *Profile) { value.Evidence[0].Observation.ObservedAt = time.Time{} }},
		{name: "unknown evidence kind", mutate: func(value *Profile) { value.Evidence[0].Observation.Evidence = "synthetic" }},
		{name: "duplicate observation signature", mutate: func(value *Profile) {
			value.Evidence[1].Observation = value.Evidence[0].Observation
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := validProfile()
			test.mutate(&profile)
			if _, err := NormalizeProfile(profile); !errors.Is(err, ErrInvalidProfile) {
				t.Fatalf("error = %v, want invalid profile", err)
			}
		})
	}
}

func TestNormalizeProfileReturnsDefensiveCollections(t *testing.T) {
	profile := validProfile()
	normalized, err := NormalizeProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	normalized.Constraints[0].AcceptedEvidence[0] = agent.EvidenceEstimated
	normalized.Evidence[0].Observation.TargetID = "mutated"
	if profile.Constraints[1].AcceptedEvidence[0] != agent.EvidenceMeasured {
		t.Fatal("normalization aliased an input accepted_evidence slice")
	}
	if profile.Evidence[1].Observation.TargetID != "node-001" {
		t.Fatal("normalization aliased an input evidence slice")
	}
}

func TestBuildRecommendationComputesDeterministicBottleneckAndReserves(t *testing.T) {
	profile := validProfile()
	draft := validRecommendationDraft(profile)
	got, err := BuildRecommendation(profile, draft)
	if err != nil {
		t.Fatalf("BuildRecommendation() error = %v", err)
	}
	if got.SchemaVersion != RecommendationSchemaV1 || !got.AdvisoryOnly {
		t.Fatalf("contract flags = %q/%v", got.SchemaVersion, got.AdvisoryOnly)
	}
	if got.SafeReserve != -50 || got.TechnicalReserve != 200 {
		t.Fatalf("workload reserves = %v/%v", got.SafeReserve, got.TechnicalReserve)
	}
	if got.Bottleneck.ConstraintID != "storage-pressure" || got.Bottleneck.EvidenceID != "storage-current" {
		t.Fatalf("bottleneck = %#v", got.Bottleneck)
	}
	if got.Bottleneck.SafeReserve != -20 || !closeFloat(got.Bottleneck.SafeReservePercent, -20.0/700.0*100) {
		t.Fatalf("bottleneck reserve = %#v", got.Bottleneck)
	}
	if got.Evidence[0].ID != "cpu-current" {
		t.Fatalf("evidence order = %#v", got.Evidence)
	}

	reordered := validRecommendationDraft(profile)
	reordered.Evidence[0], reordered.Evidence[1] = reordered.Evidence[1], reordered.Evidence[0]
	again, err := BuildRecommendation(profile, reordered)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, again) {
		t.Fatalf("input order changed result:\nfirst=%#v\nsecond=%#v", got, again)
	}
}

func TestBuildRecommendationBreaksBottleneckAndEvidenceTiesDeterministically(t *testing.T) {
	profile := validProfile()
	profile.Constraints[1].AcceptedEvidence = []agent.ObservationEvidence{agent.EvidenceMeasured, agent.EvidenceBenchmark}
	draft := validRecommendationDraft(profile)
	draft.Action = ActionReduceLoad
	draft.Evidence[0].Observation.Value = 630
	draft.Evidence = append(draft.Evidence, validEvidence(
		"aaa-cpu-current",
		agent.MetricCPUUtilization,
		"node-001",
		63,
		agent.UnitPercent,
		draft.Evidence[1].Observation.ObservedAt,
		agent.EvidenceBenchmark,
		10,
	))

	recommendation, err := BuildRecommendation(profile, draft)
	if err != nil {
		t.Fatal(err)
	}
	if recommendation.Bottleneck.ConstraintID != "cpu-pressure" {
		t.Fatalf("constraint tie selected %q, want cpu-pressure", recommendation.Bottleneck.ConstraintID)
	}
	if recommendation.Bottleneck.EvidenceID != "aaa-cpu-current" {
		t.Fatalf("observation timestamp tie selected %q, want aaa-cpu-current", recommendation.Bottleneck.EvidenceID)
	}
}

func TestBuildRecommendationAcceptsOnlyContextCompatibleActions(t *testing.T) {
	t.Run("none below safe boundary", func(t *testing.T) {
		profile := validProfile()
		draft := validRecommendationDraft(profile)
		draft.CurrentWorkload = 900
		draft.Evidence[0].Observation.Value = 600
		draft.Action = ActionNone
		if _, err := BuildRecommendation(profile, draft); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("canonical role action after breach", func(t *testing.T) {
		profile := validProfile()
		draft := validRecommendationDraft(profile)
		draft.Action = ActionAddRoleCapacity
		draft.TargetRole = " DATA-NODE "
		recommendation, err := BuildRecommendation(profile, draft)
		if err != nil {
			t.Fatal(err)
		}
		if recommendation.TargetRole != corecontracts.RoleDataNode {
			t.Fatalf("target role = %q", recommendation.TargetRole)
		}
	})

	t.Run("network action requires network bottleneck", func(t *testing.T) {
		profile := validProfile()
		profile.Constraints[0].Metric = agent.MetricNetworkThroughput
		profile.Constraints[0].TargetID = "nic-001"
		profile.Constraints[0].Unit = agent.UnitBitsPerSecond
		profile.Evidence[0] = validEvidence(
			"network-profile", agent.MetricNetworkThroughput, "nic-001", 600,
			agent.UnitBitsPerSecond, contractNow.Add(-45*time.Minute), agent.EvidenceBenchmark, 70,
		)
		draft := validRecommendationDraft(profile)
		draft.Evidence[0] = validEvidence(
			"network-current", agent.MetricNetworkThroughput, "nic-001", 720,
			agent.UnitBitsPerSecond, contractNow.Add(-5*time.Minute), agent.EvidenceMeasured, 30,
		)
		draft.Action = ActionIncreaseNetwork
		if _, err := BuildRecommendation(profile, draft); err != nil {
			t.Fatal(err)
		}
	})
}

func TestBuildRecommendationRejectsStaleForgedOrUnsafeAdvice(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Profile, *RecommendationDraft)
	}{
		{name: "missing generation guard", mutate: func(_ *Profile, value *RecommendationDraft) { value.ProfilePrecondition.Generation = nil }},
		{name: "stale profile guard", mutate: func(_ *Profile, value *RecommendationDraft) { value.ProfilePrecondition.ResourceVersion = "rv-stale" }},
		{name: "different scope", mutate: func(_ *Profile, value *RecommendationDraft) { value.ScopeID = "site-b" }},
		{name: "evaluation before calibration", mutate: func(profile *Profile, value *RecommendationDraft) {
			value.EvaluatedAt = profile.CalibratedAt.Add(-time.Second)
		}},
		{name: "future evidence", mutate: func(_ *Profile, value *RecommendationDraft) {
			value.Evidence[0].Observation.ObservedAt = value.EvaluatedAt.Add(time.Second)
		}},
		{name: "stale evidence", mutate: func(_ *Profile, value *RecommendationDraft) {
			value.Evidence[0].Observation.ObservedAt = value.EvaluatedAt.Add(-2 * time.Hour)
		}},
		{name: "missing constraint evidence", mutate: func(_ *Profile, value *RecommendationDraft) { value.Evidence = value.Evidence[:1] }},
		{name: "confidence exceeds profile", mutate: func(_ *Profile, value *RecommendationDraft) {
			value.Confidence = Confidence{Level: ConfidenceHigh, Score: 0.9}
		}},
		{name: "low confidence operational action", mutate: func(_ *Profile, value *RecommendationDraft) {
			value.Confidence = Confidence{Level: ConfidenceLow, Score: 0.4}
		}},
		{name: "wrong bottleneck action", mutate: func(_ *Profile, value *RecommendationDraft) { value.Action = ActionIncreaseNetwork }},
		{name: "role missing", mutate: func(_ *Profile, value *RecommendationDraft) { value.Action = ActionAddRoleCapacity }},
		{name: "role on non-role action", mutate: func(_ *Profile, value *RecommendationDraft) { value.TargetRole = corecontracts.RoleWorkerNode }},
		{name: "not advisory", mutate: func(_ *Profile, value *RecommendationDraft) { value.AdvisoryOnly = false }},
		{name: "unbounded summary", mutate: func(_ *Profile, value *RecommendationDraft) { value.Summary = string(make([]byte, maxSummary+1)) }},
		{name: "nan workload", mutate: func(_ *Profile, value *RecommendationDraft) { value.CurrentWorkload = math.NaN() }},
		{name: "action without breach", mutate: func(_ *Profile, value *RecommendationDraft) {
			value.CurrentWorkload = 900
			value.Evidence[0].Observation.Value = 600
			value.Action = ActionIncreaseStorage
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := validProfile()
			draft := validRecommendationDraft(profile)
			test.mutate(&profile, &draft)
			_, err := BuildRecommendation(profile, draft)
			if !errors.Is(err, ErrInvalidRecommendation) {
				t.Fatalf("error = %v, want invalid recommendation", err)
			}
		})
	}
}

func TestLowConfidenceRecommendationCanOnlyCollectEvidence(t *testing.T) {
	profile := validProfile()
	draft := validRecommendationDraft(profile)
	draft.Confidence = Confidence{Level: ConfidenceLow, Score: 0.4}
	draft.Action = ActionCollectEvidence
	got, err := BuildRecommendation(profile, draft)
	if err != nil {
		t.Fatalf("collect evidence recommendation rejected: %v", err)
	}
	if got.Action != ActionCollectEvidence {
		t.Fatalf("action = %q", got.Action)
	}
}

func TestNormalizeRecommendationRejectsForgedDerivedFieldsAndSchema(t *testing.T) {
	profile := validProfile()
	recommendation, err := BuildRecommendation(profile, validRecommendationDraft(profile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NormalizeRecommendation(recommendation, profile); err != nil {
		t.Fatalf("NormalizeRecommendation() error = %v", err)
	}
	forgeries := []struct {
		name   string
		mutate func(*Recommendation)
	}{
		{name: "safe capacity", mutate: func(value *Recommendation) { value.SafeCapacity++ }},
		{name: "safe reserve", mutate: func(value *Recommendation) { value.SafeReserve++ }},
		{name: "technical capacity", mutate: func(value *Recommendation) { value.TechnicalLimit++ }},
		{name: "technical reserve", mutate: func(value *Recommendation) { value.TechnicalReserve++ }},
		{name: "bottleneck", mutate: func(value *Recommendation) { value.Bottleneck.ObservedValue++ }},
		{name: "bottleneck reserve", mutate: func(value *Recommendation) { value.Bottleneck.SafeReservePercent++ }},
		{name: "subject", mutate: func(value *Recommendation) { value.Subject.ID = "node-999" }},
		{name: "workload unit", mutate: func(value *Recommendation) { value.WorkloadUnit = WorkloadInstances }},
	}
	for _, test := range forgeries {
		t.Run(test.name, func(t *testing.T) {
			forged := recommendation
			test.mutate(&forged)
			_, err := NormalizeRecommendation(forged, profile)
			if !errors.Is(err, ErrInvalidRecommendation) {
				t.Fatalf("error = %v, want invalid recommendation", err)
			}
		})
	}
	recommendation.SchemaVersion = "capacity.recommendation/v2"
	if _, err := NormalizeRecommendation(recommendation, profile); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("error = %v, want unsupported schema", err)
	}
}

func TestValidateRecommendationSuccessorEnforcesGeneration(t *testing.T) {
	profile := validProfile()
	current, err := BuildRecommendation(profile, validRecommendationDraft(profile))
	if err != nil {
		t.Fatal(err)
	}
	nextDraft := validRecommendationDraft(profile)
	nextDraft.ResourceVersion = "rv-recommendation-2"
	nextDraft.Generation = 2
	nextDraft.UpdatedAt = contractNow.Add(time.Minute)
	nextDraft.EvaluatedAt = contractNow.Add(time.Minute)
	nextDraft.CurrentWorkload = 1060
	nextDraft.Evidence[0].Observation.ObservedAt = contractNow
	nextDraft.Evidence[1].Observation.ObservedAt = contractNow
	next, err := BuildRecommendation(profile, nextDraft)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRecommendationSuccessor(current, next, profile); err != nil {
		t.Fatalf("ValidateRecommendationSuccessor() error = %v", err)
	}
	next.Generation = 1
	if err := ValidateRecommendationSuccessor(current, next, profile); err == nil {
		t.Fatal("changed recommendation accepted without generation increment")
	}
}
