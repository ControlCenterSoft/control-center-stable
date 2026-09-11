package capacity

import (
	"errors"
	"testing"

	"control-center/internal/corecontracts"
)

func TestBottleneckReportRanksFindingsDeterministically(t *testing.T) {
	request := BottleneckRequest{
		ScopeID:                "site-a",
		RequiredRole:           corecontracts.RoleWorkerNode,
		WorkloadUnit:           WorkloadDevices,
		FailureReserveNodes:    0,
		WarningReservePercent:  20,
		CriticalReservePercent: 5,
	}
	nodes := []NodeProjection{
		projection("node-warning", 100, 70, 12),
		projection("node-healthy", 100, 20, 50),
		projection("node-critical", 100, 95, 30),
	}

	report, err := BuildBottleneckReport(request, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != BottleneckReportSchemaV1 || report.CriticalCount != 1 || report.WarningCount != 1 || report.UnknownCount != 0 {
		t.Fatalf("unexpected bottleneck summary: %#v", report)
	}
	if report.Action != ActionAddRoleCapacity || !report.AdvisoryOnly || report.ProductionMutation {
		t.Fatalf("bottleneck report crossed advisory boundary: %#v", report)
	}
	if len(report.Findings) != 3 {
		t.Fatalf("unexpected findings: %#v", report.Findings)
	}
	if report.Findings[0].NodeID != "node-critical" || report.Findings[0].Severity != BottleneckCritical || report.Findings[0].Reason != "capacity-reserve-critical" {
		t.Fatalf("critical finding not ranked first: %#v", report.Findings[0])
	}
	if report.Findings[1].NodeID != "node-warning" || report.Findings[1].Severity != BottleneckWarning || report.Findings[1].Reason != "metric-reserve-warning" {
		t.Fatalf("warning finding not ranked second: %#v", report.Findings[1])
	}
	if report.Findings[2].NodeID != "node-healthy" || report.Findings[2].Severity != BottleneckHealthy {
		t.Fatalf("healthy finding not ranked last: %#v", report.Findings[2])
	}

	reversed, err := BuildBottleneckReport(request, []NodeProjection{nodes[2], nodes[1], nodes[0]})
	if err != nil {
		t.Fatal(err)
	}
	if report.ReportID != reversed.ReportID {
		t.Fatalf("report identity depends on input order: %q %q", report.ReportID, reversed.ReportID)
	}
	for i := range report.Findings {
		if report.Findings[i] != reversed.Findings[i] {
			t.Fatalf("finding %d depends on input order: %#v %#v", i, report.Findings[i], reversed.Findings[i])
		}
	}
}

func TestBottleneckReportRequestsEvidenceForLowConfidence(t *testing.T) {
	request := BottleneckRequest{
		ScopeID:                "site-a",
		RequiredRole:           corecontracts.RoleWorkerNode,
		WorkloadUnit:           WorkloadDevices,
		WarningReservePercent:  20,
		CriticalReservePercent: 5,
	}
	node := projection("node-a", 100, 20, 50)
	node.Confidence = Confidence{Level: ConfidenceLow, Score: .4}

	report, err := BuildBottleneckReport(request, []NodeProjection{node})
	if err != nil {
		t.Fatal(err)
	}
	if report.UnknownCount != 1 || report.Action != ActionCollectEvidence {
		t.Fatalf("low confidence was not held for evidence: %#v", report)
	}
	if len(report.Findings) != 1 || report.Findings[0].Severity != BottleneckUnknown || report.Findings[0].Reason != "insufficient-confidence" {
		t.Fatalf("unexpected low-confidence finding: %#v", report.Findings)
	}
}

func TestBottleneckReportRejectsInvalidThresholdOrder(t *testing.T) {
	_, err := BuildBottleneckReport(BottleneckRequest{
		ScopeID:                "site-a",
		RequiredRole:           corecontracts.RoleWorkerNode,
		WorkloadUnit:           WorkloadDevices,
		WarningReservePercent:  20,
		CriticalReservePercent: 30,
	}, []NodeProjection{projection("node-a", 100, 20, 50)})
	if !errors.Is(err, ErrInvalidRecommendation) {
		t.Fatalf("expected invalid recommendation, got %v", err)
	}
}
