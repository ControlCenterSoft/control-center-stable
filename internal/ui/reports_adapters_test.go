package ui

import (
	"strings"
	"testing"
	"time"

	"control-center/internal/corecontracts"
	"control-center/internal/incidents"
)

func TestBuildHealthOperationalReportPreservesFailClosedFreshness(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	overview, err := BuildHealthOverview(HealthOverviewInput{
		Loaded:       true,
		Now:          now,
		StaleAfter:   5 * time.Minute,
		ExpiredAfter: 20 * time.Minute,
		Signals: []HealthSignal{{
			ID: "readyz-node-1", ResourceKind: "node", ResourceID: "node-1",
			CheckName: "readyz", State: HealthStateHealthy,
			ObservedAt: now.Add(-10 * time.Minute),
			RunbookRef: "runbook:node-readiness", EvidenceRefs: []string{"evidence:readyz:1"},
		}},
	})
	if err != nil {
		t.Fatalf("BuildHealthOverview() error = %v", err)
	}

	report, err := BuildHealthOperationalReport(now, overview)
	if err != nil {
		t.Fatalf("BuildHealthOperationalReport() error = %v", err)
	}
	if report.OverallState != ReportHealthDegraded {
		t.Fatalf("OverallState = %q, want %q", report.OverallState, ReportHealthDegraded)
	}
	if report.MutationAuthorized {
		t.Fatal("MutationAuthorized = true, want false")
	}
	if len(report.Evidence) != 1 || report.Evidence[0].Freshness != ReportFreshnessStale {
		t.Fatalf("Evidence = %#v", report.Evidence)
	}
	if report.Evidence[0].EffectiveState != ReportHealthDegraded || report.Evidence[0].ObservedState != ReportHealthHealthy {
		t.Fatalf("adapted states = observed %q effective %q", report.Evidence[0].ObservedState, report.Evidence[0].EffectiveState)
	}
}

func TestBuildHealthOperationalReportRejectsTamperedEffectiveState(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	overview, err := BuildHealthOverview(HealthOverviewInput{
		Loaded: true, Now: now, StaleAfter: time.Minute, ExpiredAfter: 5 * time.Minute,
		Signals: []HealthSignal{{
			ID: "readyz-node-1", ResourceKind: "node", ResourceID: "node-1",
			CheckName: "readyz", State: HealthStateHealthy, ObservedAt: now.Add(-2 * time.Minute),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	overview.Signals[0].EffectiveState = HealthStateHealthy
	if _, err := BuildHealthOperationalReport(now, overview); err == nil {
		t.Fatal("BuildHealthOperationalReport() error = nil, want fail-closed rejection")
	}
}

func TestBuildHealthOperationalReportUnavailableRemainsUnknown(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	overview, err := BuildHealthOverview(HealthOverviewInput{
		Loaded: false, Now: now, StaleAfter: time.Minute, ExpiredAfter: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := BuildHealthOperationalReport(now, overview)
	if err != nil {
		t.Fatal(err)
	}
	if report.DataState != ReportDataUnavailable || report.OverallState != ReportHealthUnknown || len(report.Evidence) != 0 {
		t.Fatalf("unexpected unavailable report: %#v", report)
	}
}

func TestBuildIncidentOperationalReportCriticalIsUnhealthyAndResourceBound(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	incident := reportTestIncident(incidents.StatusOpen, incidents.SeverityCritical)
	incident.AffectedResources = append(incident.AffectedResources,
		incidents.ResourceRef{Kind: "service", ID: "postgresql", ScopeID: "site:primary"},
	)

	report, err := BuildIncidentOperationalReport(now, incidents.ListPage{Items: []incidents.Incident{incident}})
	if err != nil {
		t.Fatalf("BuildIncidentOperationalReport() error = %v", err)
	}
	if report.OverallState != ReportHealthUnhealthy || len(report.Evidence) != 2 {
		t.Fatalf("report = %#v", report)
	}
	if report.MutationAuthorized {
		t.Fatal("MutationAuthorized = true, want false")
	}

	drawer, err := BuildEvidenceDrawer(report, "node", "node-1")
	if err != nil {
		t.Fatalf("BuildEvidenceDrawer() error = %v", err)
	}
	if len(drawer.Evidence) != 1 || drawer.Evidence[0].ResourceID != "node-1" {
		t.Fatalf("drawer leaked cross-resource evidence: %#v", drawer.Evidence)
	}
	if drawer.OverallState != ReportHealthUnhealthy || drawer.MutationAuthorized {
		t.Fatalf("drawer state = %#v", drawer)
	}
}

func TestBuildIncidentOperationalReportResolvedDoesNotInventHealthy(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	incident := reportTestIncident(incidents.StatusResolved, incidents.SeverityWarning)
	report, err := BuildIncidentOperationalReport(now, incidents.ListPage{Items: []incidents.Incident{incident}})
	if err != nil {
		t.Fatal(err)
	}
	if report.OverallState != ReportHealthUnknown || report.Evidence[0].ObservedState != ReportHealthUnknown {
		t.Fatalf("resolved incident invented healthy state: %#v", report)
	}
}

func TestBuildIncidentOperationalReportRejectsPartialPage(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	incident := reportTestIncident(incidents.StatusOpen, incidents.SeverityWarning)
	page := incidents.ListPage{
		Items: []incidents.Incident{incident}, HasMore: true,
		Next: &incidents.ListCursor{StartedAt: incident.StartedAt, ObjectID: incident.ObjectID},
	}
	if _, err := BuildIncidentOperationalReport(now, page); err == nil {
		t.Fatal("BuildIncidentOperationalReport() error = nil, want partial-page rejection")
	}
}

func TestBuildIncidentOperationalReportRejectsInvalidStoredIncident(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	incident := reportTestIncident(incidents.StatusOpen, incidents.SeverityWarning)
	incident.Signals[0].Summary = strings.Repeat("x", 513)
	if _, err := BuildIncidentOperationalReport(now, incidents.ListPage{Items: []incidents.Incident{incident}}); err == nil {
		t.Fatal("BuildIncidentOperationalReport() error = nil, want invalid-source rejection")
	}
}

func reportTestIncident(status incidents.Status, severity incidents.Severity) incidents.Incident {
	started := time.Date(2026, 9, 12, 23, 0, 0, 0, time.UTC)
	observed := started.Add(2 * time.Minute)
	ackAt := started.Add(3 * time.Minute)
	updated := observed
	evidence := incidents.EvidenceRef{
		Kind: "health-evidence", ID: "evidence:node-1", SHA256: strings.Repeat("a", 64),
		Collected: observed, Redaction: incidents.RedactionApplied,
	}
	incident := incidents.Incident{
		ObjectMetadata: corecontracts.ObjectMetadata{
			ObjectID: "incident:report-1", ScopeID: "site:primary", OwnerScope: "installation:default",
			Generation: 1, ResourceVersion: "rv-report-1", CreatedAt: started, UpdatedAt: updated,
		},
		Severity: severity, Status: status, Title: "Проверочный инцидент",
		StartedAt: started, LastObservedAt: observed,
		AffectedResources: []incidents.ResourceRef{{Kind: "node", ID: "node-1", ScopeID: "site:primary"}},
		Signals: []incidents.Signal{{
			ID: "signal-report-1", Kind: "readiness", Source: "core-health", ObservedAt: observed,
			Summary: "Проверка состояния узла", Evidence: []incidents.EvidenceRef{evidence},
		}},
		Runbook:  &incidents.RunbookRef{ID: "node-readiness", Revision: "v1"},
		Evidence: []incidents.EvidenceRef{evidence},
		Timeline: []incidents.TimelineEntry{
			{Kind: incidents.TimelineOpened, At: started},
			{Kind: incidents.TimelineSignalObserved, At: observed, Summary: "Получен health signal", Evidence: []incidents.EvidenceRef{evidence}},
		},
	}
	if status == incidents.StatusAcknowledged || status == incidents.StatusResolved {
		incident.Acknowledgement = &incidents.Acknowledgement{ActorID: "user:admin", At: ackAt}
		incident.Timeline = append(incident.Timeline, incidents.TimelineEntry{
			Kind: incidents.TimelineAcknowledged, At: ackAt, ActorID: "user:admin", Summary: "Инцидент принят в работу",
		})
		incident.UpdatedAt = ackAt
		incident.Generation = 2
		incident.ResourceVersion = "rv-report-2"
	}
	if status == incidents.StatusResolved {
		resolvedAt := ackAt.Add(time.Minute)
		incident.Timeline = append(incident.Timeline, incidents.TimelineEntry{
			Kind: incidents.TimelineResolved, At: resolvedAt, ActorID: "user:admin", Summary: "Инцидент закрыт",
		})
		incident.UpdatedAt = resolvedAt
		incident.Generation = 3
		incident.ResourceVersion = "rv-report-3"
	}
	return incident
}
