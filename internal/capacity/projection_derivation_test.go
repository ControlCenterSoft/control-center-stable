package capacity

import (
	"errors"
	"math"
	"testing"
	"time"

	"control-center/internal/agent"
	"control-center/internal/corecontracts"
)

func projectionDerivationTelemetry() NodeProjectionTelemetry {
	return NodeProjectionTelemetry{
		SnapshotID:      "telemetry-node-001",
		Revision:        "rv-telemetry-7",
		NodeID:          "node-001",
		Role:            corecontracts.RoleWorkerNode,
		Healthy:         true,
		ObservedAt:      contractNow,
		CurrentWorkload: 620,
		WorkloadUnit:    WorkloadDevices,
		Observations: []agent.CapacityObservation{
			{
				Metric: agent.MetricStorageUsed, TargetID: "disk-001", Value: 650,
				Unit: agent.UnitBytes, ObservedAt: contractNow.Add(-2 * time.Minute),
				Evidence: agent.EvidenceMeasured,
			},
			{
				Metric: agent.MetricCPUUtilization, TargetID: "node-001", Value: 63,
				Unit: agent.UnitPercent, ObservedAt: contractNow.Add(-time.Minute),
				Evidence: agent.EvidenceMeasured,
			},
		},
	}
}

func TestDeriveNodeProjectionBindsExactProfileAndTelemetry(t *testing.T) {
	profile := validProfile()
	telemetry := projectionDerivationTelemetry()
	evaluatedAt := contractNow.Add(5 * time.Minute)

	got, err := DeriveNodeProjection(profile, telemetry, evaluatedAt)
	if err != nil {
		t.Fatalf("DeriveNodeProjection() error = %v", err)
	}
	if got.SchemaVersion != NodeProjectionDerivationSchemaV1 || got.DerivationID == "" {
		t.Fatalf("unexpected derivation identity: %#v", got)
	}
	if got.ProfileObjectID != profile.ObjectID || got.ProfileGeneration != profile.Generation ||
		got.ProfileResourceVersion != profile.ResourceVersion {
		t.Fatalf("profile binding mismatch: %#v", got)
	}
	if got.TelemetrySnapshotID != telemetry.SnapshotID || got.TelemetryRevision != telemetry.Revision {
		t.Fatalf("telemetry binding mismatch: %#v", got)
	}
	if got.Projection.NodeID != "node-001" || got.Projection.Role != corecontracts.RoleWorkerNode ||
		!got.Projection.Healthy || got.Projection.CurrentWorkload != 620 {
		t.Fatalf("runtime projection mismatch: %#v", got.Projection)
	}
	if got.Projection.SafeCapacity != profile.SafeCapacity || got.Projection.TechnicalLimit != profile.TechnicalLimit ||
		got.Projection.Confidence != profile.Confidence {
		t.Fatalf("profile-derived fields mismatch: %#v", got.Projection)
	}
	if got.Projection.BottleneckMetric != agent.MetricStorageUsed || got.Projection.BottleneckTargetID != "disk-001" {
		t.Fatalf("bottleneck selection = %#v", got.Projection)
	}
	wantReserve := (700.0 - 650.0) / 700.0 * 100
	if math.Abs(got.Projection.BottleneckReserve-wantReserve) > 1e-9 {
		t.Fatalf("bottleneck reserve = %v, want %v", got.Projection.BottleneckReserve, wantReserve)
	}
	if !got.AdvisoryOnly || got.ProductionMutation {
		t.Fatalf("unsafe projection evidence: %#v", got)
	}
	if err := ValidateNodeProjectionDerivation(got, profile, telemetry, evaluatedAt); err != nil {
		t.Fatalf("ValidateNodeProjectionDerivation() error = %v", err)
	}
}

func TestDeriveNodeProjectionIsDeterministicAcrossObservationOrder(t *testing.T) {
	profile := validProfile()
	telemetry := projectionDerivationTelemetry()
	evaluatedAt := contractNow.Add(5 * time.Minute)
	first, err := DeriveNodeProjection(profile, telemetry, evaluatedAt)
	if err != nil {
		t.Fatalf("first derivation error = %v", err)
	}
	telemetry.Observations[0], telemetry.Observations[1] = telemetry.Observations[1], telemetry.Observations[0]
	second, err := DeriveNodeProjection(profile, telemetry, evaluatedAt)
	if err != nil {
		t.Fatalf("second derivation error = %v", err)
	}
	if first != second {
		t.Fatalf("observation ordering changed derivation:\nfirst=%#v\nsecond=%#v", first, second)
	}
}

func TestDeriveNodeProjectionRejectsIncompleteOrCallerSelectedTelemetry(t *testing.T) {
	profile := validProfile()
	evaluatedAt := contractNow.Add(5 * time.Minute)

	tests := []struct {
		name   string
		mutate func(*NodeProjectionTelemetry)
	}{
		{name: "missing constraint", mutate: func(value *NodeProjectionTelemetry) { value.Observations = value.Observations[:1] }},
		{name: "duplicate constraint", mutate: func(value *NodeProjectionTelemetry) { value.Observations[1] = value.Observations[0] }},
		{name: "wrong node", mutate: func(value *NodeProjectionTelemetry) { value.NodeID = "node-002" }},
		{name: "wrong workload unit", mutate: func(value *NodeProjectionTelemetry) { value.WorkloadUnit = WorkloadInstances }},
		{name: "invalid role", mutate: func(value *NodeProjectionTelemetry) { value.Role = "root" }},
		{name: "nan workload", mutate: func(value *NodeProjectionTelemetry) { value.CurrentWorkload = math.NaN() }},
		{name: "unaccepted evidence", mutate: func(value *NodeProjectionTelemetry) { value.Observations[1].Evidence = agent.EvidenceBenchmark }},
		{name: "observation after snapshot", mutate: func(value *NodeProjectionTelemetry) {
			value.Observations[0].ObservedAt = value.ObservedAt.Add(time.Second)
		}},
		{name: "stale constraint observation", mutate: func(value *NodeProjectionTelemetry) {
			value.Observations[0].ObservedAt = value.ObservedAt.Add(-2 * time.Hour)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			telemetry := projectionDerivationTelemetry()
			test.mutate(&telemetry)
			if _, err := DeriveNodeProjection(profile, telemetry, evaluatedAt); !errors.Is(err, ErrInvalidRecommendation) {
				t.Fatalf("error = %v, want invalid recommendation", err)
			}
		})
	}
}

func TestDeriveNodeProjectionRejectsStaleSnapshotAndNonNodeProfile(t *testing.T) {
	profile := validProfile()
	telemetry := projectionDerivationTelemetry()
	if _, err := DeriveNodeProjection(profile, telemetry, telemetry.ObservedAt.Add(16*time.Minute)); !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("stale snapshot error = %v", err)
	}

	profile = validProfile()
	profile.Subject = Subject{Kind: SubjectRole, ID: "worker-pool-a", Role: corecontracts.RoleWorkerNode}
	if _, err := DeriveNodeProjection(profile, telemetry, contractNow.Add(5*time.Minute)); !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("role profile error = %v", err)
	}
}

func TestValidateNodeProjectionDerivationRejectsDriftAndTampering(t *testing.T) {
	profile := validProfile()
	telemetry := projectionDerivationTelemetry()
	evaluatedAt := contractNow.Add(5 * time.Minute)
	saved, err := DeriveNodeProjection(profile, telemetry, evaluatedAt)
	if err != nil {
		t.Fatalf("DeriveNodeProjection() error = %v", err)
	}

	profileDrift := profile
	profileDrift.ResourceVersion = "rv-profile-2"
	if err := ValidateNodeProjectionDerivation(saved, profileDrift, telemetry, evaluatedAt); !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("profile revision drift error = %v", err)
	}

	telemetryDrift := telemetry
	telemetryDrift.Revision = "rv-telemetry-8"
	if err := ValidateNodeProjectionDerivation(saved, profile, telemetryDrift, evaluatedAt); !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("telemetry revision drift error = %v", err)
	}

	tampered := saved
	tampered.Projection.SafeCapacity++
	if err := ValidateNodeProjectionDerivation(tampered, profile, telemetry, evaluatedAt); !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("tampered projection error = %v", err)
	}

	unsafe := saved
	unsafe.ProductionMutation = true
	if err := ValidateNodeProjectionDerivation(unsafe, profile, telemetry, evaluatedAt); !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("unsafe evidence error = %v", err)
	}
}
