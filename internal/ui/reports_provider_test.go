package ui

import (
	"strings"
	"testing"
	"time"
)

func TestValidateOperationalReportAcceptsCanonicalProjection(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 40, 0, 0, time.UTC)
	report, err := BuildOperationalReport(now, ReportDataLoaded, []ReportEvidenceInput{{
		ID:             "health:node-1",
		Kind:           "health",
		ResourceKind:   "node",
		ResourceID:     "node-1",
		ObservedState:  ReportHealthDegraded,
		Freshness:      ReportFreshnessCurrent,
		ObservedAt:     now.Add(-time.Minute),
		ReasonCode:     "health_degraded",
		EvidenceRefs:   []string{"health:node-1:sample"},
		EvidenceDigest: "sha256:" + strings.Repeat("a", 64),
	}})
	if err != nil {
		t.Fatalf("BuildOperationalReport() error = %v", err)
	}
	if err := ValidateOperationalReport(report); err != nil {
		t.Fatalf("ValidateOperationalReport() error = %v", err)
	}
}

func TestValidateOperationalReportRejectsTamperedEffectiveState(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 40, 0, 0, time.UTC)
	report, err := BuildOperationalReport(now, ReportDataLoaded, []ReportEvidenceInput{{
		ID:            "health:node-1",
		Kind:          "health",
		ResourceKind:  "node",
		ResourceID:    "node-1",
		ObservedState: ReportHealthHealthy,
		Freshness:     ReportFreshnessStale,
		ObservedAt:    now.Add(-time.Minute),
	}})
	if err != nil {
		t.Fatalf("BuildOperationalReport() error = %v", err)
	}
	report.Evidence[0].EffectiveState = ReportHealthHealthy
	report.OverallState = ReportHealthHealthy
	if err := ValidateOperationalReport(report); err == nil {
		t.Fatal("ValidateOperationalReport() error = nil, want tampered effective-state rejection")
	}
}

func TestValidateOperationalReportRejectsDigestOrOrderingDrift(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 40, 0, 0, time.UTC)
	report, err := BuildOperationalReport(now, ReportDataLoaded, []ReportEvidenceInput{
		{
			ID:            "health:node-a",
			Kind:          "health",
			ResourceKind:  "node",
			ResourceID:    "node-a",
			ObservedState: ReportHealthUnknown,
			Freshness:     ReportFreshnessCurrent,
			ObservedAt:    now.Add(-time.Minute),
		},
		{
			ID:            "health:node-b",
			Kind:          "health",
			ResourceKind:  "node",
			ResourceID:    "node-b",
			ObservedState: ReportHealthUnhealthy,
			Freshness:     ReportFreshnessCurrent,
			ObservedAt:    now.Add(-time.Minute),
		},
	})
	if err != nil {
		t.Fatalf("BuildOperationalReport() error = %v", err)
	}

	t.Run("digest", func(t *testing.T) {
		tampered := report
		tampered.Evidence = append([]ReportEvidence(nil), report.Evidence...)
		tampered.EvidenceDigest = "sha256:" + strings.Repeat("f", 64)
		if err := ValidateOperationalReport(tampered); err == nil {
			t.Fatal("ValidateOperationalReport() error = nil, want digest rejection")
		}
	})

	t.Run("ordering", func(t *testing.T) {
		tampered := report
		tampered.Evidence = append([]ReportEvidence(nil), report.Evidence...)
		tampered.Evidence[0], tampered.Evidence[1] = tampered.Evidence[1], tampered.Evidence[0]
		if err := ValidateOperationalReport(tampered); err == nil {
			t.Fatal("ValidateOperationalReport() error = nil, want non-canonical ordering rejection")
		}
	})
}

func TestValidateOperationalReportRejectsMutationAuthorityAndImplicitEvidence(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 40, 0, 0, time.UTC)
	report, err := BuildOperationalReport(now, ReportDataUnavailable, nil)
	if err != nil {
		t.Fatalf("BuildOperationalReport() error = %v", err)
	}

	withAuthority := report
	withAuthority.MutationAuthorized = true
	if err := ValidateOperationalReport(withAuthority); err == nil {
		t.Fatal("ValidateOperationalReport() error = nil, want mutation-authority rejection")
	}

	implicitEvidence := report
	implicitEvidence.Evidence = nil
	if err := ValidateOperationalReport(implicitEvidence); err == nil {
		t.Fatal("ValidateOperationalReport() error = nil, want explicit-array rejection")
	}
}
