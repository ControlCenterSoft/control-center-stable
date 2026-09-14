package ui

import (
	"strings"
	"testing"
	"time"
)

func TestBuildOperationalReportFailClosedFreshnessAndDeterministicOrder(t *testing.T) {
	now := time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC)
	input := []ReportEvidenceInput{
		{
			ID: "network-a", Kind: "network-verification", ResourceKind: "node", ResourceID: "node-b",
			ObservedState: ReportHealthHealthy, Freshness: ReportFreshnessExpired, ObservedAt: now.Add(-2 * time.Hour),
			ReasonCode: "network.verification.expired", EvidenceRefs: []string{"evidence:z", "evidence:a"},
		},
		{
			ID: "audit-a", Kind: "audit-integrity", ResourceKind: "installation", ResourceID: "local",
			ObservedState: ReportHealthHealthy, Freshness: ReportFreshnessCurrent, ObservedAt: now.Add(-time.Minute),
			ReasonCode: "audit.integrity.confirmed",
		},
		{
			ID: "restore-a", Kind: "restore-freshness", ResourceKind: "node", ResourceID: "node-a",
			ObservedState: ReportHealthHealthy, Freshness: ReportFreshnessStale, ObservedAt: now.Add(-time.Hour),
			ReasonCode: "restore.drill.stale",
		},
	}

	got, err := BuildOperationalReport(now, ReportDataLoaded, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.MutationAuthorized {
		t.Fatal("report unexpectedly authorizes mutation")
	}
	if got.OverallState != ReportHealthDegraded {
		t.Fatalf("overall state = %q, want degraded", got.OverallState)
	}
	if len(got.Evidence) != 3 {
		t.Fatalf("evidence count = %d", len(got.Evidence))
	}
	if got.Evidence[0].ID != "restore-a" || got.Evidence[0].EffectiveState != ReportHealthDegraded {
		t.Fatalf("first evidence = %#v", got.Evidence[0])
	}
	if got.Evidence[1].ID != "network-a" || got.Evidence[1].EffectiveState != ReportHealthUnknown {
		t.Fatalf("second evidence = %#v", got.Evidence[1])
	}
	if got.Evidence[2].ID != "audit-a" || got.Evidence[2].EffectiveState != ReportHealthHealthy {
		t.Fatalf("third evidence = %#v", got.Evidence[2])
	}
	if strings.Join(got.Evidence[1].EvidenceRefs, ",") != "evidence:a,evidence:z" {
		t.Fatalf("evidence refs are not deterministic: %#v", got.Evidence[1].EvidenceRefs)
	}
	if !strings.HasPrefix(got.EvidenceDigest, "sha256:") {
		t.Fatalf("unexpected digest %q", got.EvidenceDigest)
	}

	reversed := []ReportEvidenceInput{input[2], input[1], input[0]}
	again, err := BuildOperationalReport(now, ReportDataLoaded, reversed)
	if err != nil {
		t.Fatal(err)
	}
	if got.EvidenceDigest != again.EvidenceDigest {
		t.Fatalf("digest changed with input order: %q != %q", got.EvidenceDigest, again.EvidenceDigest)
	}
}

func TestBuildOperationalReportUnavailableAndEmptyNeverHealthy(t *testing.T) {
	now := time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC)
	for _, state := range []ReportDataState{ReportDataUnavailable, ReportDataLoaded} {
		got, err := BuildOperationalReport(now, state, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.OverallState != ReportHealthUnknown {
			t.Fatalf("state %q became %q", state, got.OverallState)
		}
		if got.MutationAuthorized {
			t.Fatal("empty/unavailable report unexpectedly authorizes mutation")
		}
	}
}

func TestBuildOperationalReportRejectsAmbiguousOrUnsafeEvidence(t *testing.T) {
	now := time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC)
	base := ReportEvidenceInput{
		ID: "audit-a", Kind: "audit-integrity", ResourceKind: "installation", ResourceID: "local",
		ObservedState: ReportHealthHealthy, Freshness: ReportFreshnessCurrent, ObservedAt: now.Add(-time.Minute),
		ReasonCode: "audit.integrity.confirmed",
	}

	cases := []ReportEvidenceInput{
		func() ReportEvidenceInput { v := base; v.ObservedAt = now.Add(time.Second); return v }(),
		func() ReportEvidenceInput { v := base; v.ReasonCode = "raw error text"; return v }(),
		func() ReportEvidenceInput { v := base; v.EvidenceDigest = "sha256:ABC"; return v }(),
		func() ReportEvidenceInput { v := base; v.EvidenceRefs = []string{"same", "same"}; return v }(),
		func() ReportEvidenceInput { v := base; v.ResourceID = "node\nsecret"; return v }(),
	}
	for i, candidate := range cases {
		if _, err := BuildOperationalReport(now, ReportDataLoaded, []ReportEvidenceInput{candidate}); err == nil {
			t.Fatalf("case %d was accepted", i)
		}
	}

	if _, err := BuildOperationalReport(now, ReportDataUnavailable, []ReportEvidenceInput{base}); err == nil {
		t.Fatal("unavailable report with evidence was accepted")
	}
	if _, err := BuildOperationalReport(now, ReportDataLoaded, []ReportEvidenceInput{base, base}); err == nil {
		t.Fatal("duplicate evidence ids were accepted")
	}
}

func TestBuildEvidenceDrawerIsReadOnlyAndResourceBound(t *testing.T) {
	now := time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC)
	report, err := BuildOperationalReport(now, ReportDataLoaded, []ReportEvidenceInput{
		{
			ID: "health-node-a", Kind: "health", ResourceKind: "node", ResourceID: "node-a",
			ObservedState: ReportHealthHealthy, Freshness: ReportFreshnessCurrent, ObservedAt: now.Add(-time.Minute),
			ReasonCode: "health.confirmed", RunbookRef: "runbook:node-health",
		},
		{
			ID: "restore-node-a", Kind: "restore-freshness", ResourceKind: "node", ResourceID: "node-a",
			ObservedState: ReportHealthHealthy, Freshness: ReportFreshnessStale, ObservedAt: now.Add(-time.Hour),
			ReasonCode: "restore.drill.stale",
		},
		{
			ID: "health-node-b", Kind: "health", ResourceKind: "node", ResourceID: "node-b",
			ObservedState: ReportHealthUnhealthy, Freshness: ReportFreshnessCurrent, ObservedAt: now.Add(-time.Minute),
			ReasonCode: "health.failed",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	drawer, err := BuildEvidenceDrawer(report, "node", "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if drawer.MutationAuthorized {
		t.Fatal("drawer unexpectedly authorizes mutation")
	}
	if drawer.OverallState != ReportHealthDegraded || len(drawer.Evidence) != 2 {
		t.Fatalf("drawer = %#v", drawer)
	}
	for _, item := range drawer.Evidence {
		if item.ResourceID != "node-a" {
			t.Fatalf("cross-resource evidence leaked: %#v", item)
		}
	}

	missing, err := BuildEvidenceDrawer(report, "node", "missing")
	if err != nil {
		t.Fatal(err)
	}
	if missing.OverallState != ReportHealthUnknown || len(missing.Evidence) != 0 {
		t.Fatalf("missing drawer became healthy: %#v", missing)
	}
}
