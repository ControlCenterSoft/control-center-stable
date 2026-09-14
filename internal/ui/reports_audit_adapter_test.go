package ui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"control-center/internal/identity/audit"
)

func TestBuildAuditOperationalReportIsExactBoundAndNeverHealthyFromAuditAlone(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	failedHash := strings.Repeat("a", 64)
	successHash := strings.Repeat("b", 64)
	page := audit.Page{Entries: []audit.Entry{
		{
			SequenceID: 11,
			Event: audit.Event{
				ID:         "11111111-1111-4111-8111-111111111111",
				OccurredAt: now.Add(-time.Minute),
				Action:     "incidents.acknowledge",
				Outcome:    "failed",
				ActorID:    "operator-a",
				SubjectID:  "incident-a",
				SourceIP:   "192.0.2.10",
				Details:    map[string]any{"token": "must-not-project", "reason": "test"},
				Hash:       failedHash,
			},
		},
		{
			SequenceID: 10,
			Event: audit.Event{
				ID:         "22222222-2222-4222-8222-222222222222",
				OccurredAt: now.Add(-2 * time.Minute),
				Action:     "incidents.resolve",
				Outcome:    "success",
				ActorID:    "operator-b",
				SubjectID:  "incident-b",
				Hash:       successHash,
			},
		},
	}}
	bindings := []AuditReportBinding{
		{SequenceID: 11, EventID: page.Entries[0].Event.ID, EventHash: failedHash, ResourceKind: "incident", ResourceID: "incident-a"},
		{SequenceID: 10, EventID: page.Entries[1].Event.ID, EventHash: successHash, ResourceKind: "incident", ResourceID: "incident-b"},
	}

	report, err := BuildAuditOperationalReport(now, page, bindings)
	if err != nil {
		t.Fatalf("BuildAuditOperationalReport() error = %v", err)
	}
	if report.MutationAuthorized {
		t.Fatal("audit report unexpectedly authorizes mutation")
	}
	if report.OverallState != ReportHealthDegraded {
		t.Fatalf("overall state = %q, want %q", report.OverallState, ReportHealthDegraded)
	}
	if len(report.Evidence) != 2 {
		t.Fatalf("evidence count = %d, want 2", len(report.Evidence))
	}
	if report.Evidence[0].ObservedState != ReportHealthDegraded || report.Evidence[0].ResourceID != "incident-a" {
		t.Fatalf("first evidence = %#v", report.Evidence[0])
	}
	if report.Evidence[1].ObservedState != ReportHealthUnknown || report.Evidence[1].EffectiveState != ReportHealthUnknown {
		t.Fatalf("success audit evidence must remain unknown, got %#v", report.Evidence[1])
	}
	if report.Evidence[0].EvidenceDigest != "sha256:"+failedHash || report.Evidence[1].EvidenceDigest != "sha256:"+successHash {
		t.Fatalf("unexpected evidence digests: %#v", report.Evidence)
	}

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{"192.0.2.10", "must-not-project", "operator-a", "operator-b"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("private Audit field leaked into report: %q", forbidden)
		}
	}
}

func TestBuildAuditOperationalReportEmptyCompletePageIsUnknownNotHealthy(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	report, err := BuildAuditOperationalReport(now, audit.Page{}, nil)
	if err != nil {
		t.Fatalf("BuildAuditOperationalReport() error = %v", err)
	}
	if report.DataState != ReportDataLoaded || report.OverallState != ReportHealthUnknown || len(report.Evidence) != 0 {
		t.Fatalf("empty audit report = %#v", report)
	}
}

func TestBuildAuditOperationalReportRejectsPartialPage(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	_, err := BuildAuditOperationalReport(now, audit.Page{HasMore: true}, nil)
	if err == nil || !strings.Contains(err.Error(), "complete bounded page") {
		t.Fatalf("error = %v, want partial-page rejection", err)
	}
}

func TestBuildAuditOperationalReportRequiresOneBindingPerEntry(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	page := audit.Page{Entries: []audit.Entry{{SequenceID: 1, Event: audit.Event{
		ID: "11111111-1111-4111-8111-111111111111", OccurredAt: now, Action: "incidents.resolve", Outcome: "success", SubjectID: "incident-a", Hash: strings.Repeat("a", 64),
	}}}}
	_, err := BuildAuditOperationalReport(now, page, nil)
	if err == nil || !strings.Contains(err.Error(), "one exact resource binding") {
		t.Fatalf("error = %v, want binding-count rejection", err)
	}
}

func TestBuildAuditOperationalReportRejectsCrossResourceBinding(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	hash := strings.Repeat("a", 64)
	entry := audit.Entry{SequenceID: 7, Event: audit.Event{
		ID: "11111111-1111-4111-8111-111111111111", OccurredAt: now, Action: "incidents.resolve", Outcome: "success", SubjectID: "incident-a", Hash: hash,
	}}
	_, err := BuildAuditOperationalReport(now, audit.Page{Entries: []audit.Entry{entry}}, []AuditReportBinding{{
		SequenceID: 7, EventID: entry.Event.ID, EventHash: hash, ResourceKind: "incident", ResourceID: "incident-b",
	}})
	if err == nil || !strings.Contains(err.Error(), "subject/resource binding mismatch") {
		t.Fatalf("error = %v, want cross-resource rejection", err)
	}
}

func TestBuildAuditOperationalReportRejectsEventIdentityDrift(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	hash := strings.Repeat("a", 64)
	entry := audit.Entry{SequenceID: 7, Event: audit.Event{
		ID: "11111111-1111-4111-8111-111111111111", OccurredAt: now, Action: "incidents.resolve", Outcome: "success", SubjectID: "incident-a", Hash: hash,
	}}
	_, err := BuildAuditOperationalReport(now, audit.Page{Entries: []audit.Entry{entry}}, []AuditReportBinding{{
		SequenceID: 7, EventID: "22222222-2222-4222-8222-222222222222", EventHash: hash, ResourceKind: "incident", ResourceID: "incident-a",
	}})
	if err == nil || !strings.Contains(err.Error(), "binding identity mismatch") {
		t.Fatalf("error = %v, want identity mismatch rejection", err)
	}
}

func TestBuildAuditOperationalReportRejectsInvalidHashAndUnsupportedOutcome(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	entry := audit.Entry{SequenceID: 7, Event: audit.Event{
		ID: "11111111-1111-4111-8111-111111111111", OccurredAt: now, Action: "incidents.resolve", Outcome: "mystery", SubjectID: "incident-a", Hash: strings.Repeat("a", 64),
	}}
	binding := AuditReportBinding{SequenceID: 7, EventID: entry.Event.ID, EventHash: entry.Event.Hash, ResourceKind: "incident", ResourceID: "incident-a"}
	_, err := BuildAuditOperationalReport(now, audit.Page{Entries: []audit.Entry{entry}}, []AuditReportBinding{binding})
	if err == nil || !strings.Contains(err.Error(), "unsupported audit outcome") {
		t.Fatalf("error = %v, want outcome rejection", err)
	}

	entry.Event.Outcome = "success"
	entry.Event.Hash = "ABC"
	binding.EventHash = "ABC"
	_, err = BuildAuditOperationalReport(now, audit.Page{Entries: []audit.Entry{entry}}, []AuditReportBinding{binding})
	if err == nil || !strings.Contains(err.Error(), "event_hash") {
		t.Fatalf("error = %v, want hash rejection", err)
	}
}

func TestBuildAuditOperationalReportRejectsDuplicateBindingAndFutureEvidence(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	hash := strings.Repeat("a", 64)
	entry := audit.Entry{SequenceID: 7, Event: audit.Event{
		ID: "11111111-1111-4111-8111-111111111111", OccurredAt: now.Add(time.Minute), Action: "incidents.resolve", Outcome: "success", SubjectID: "incident-a", Hash: hash,
	}}
	binding := AuditReportBinding{SequenceID: 7, EventID: entry.Event.ID, EventHash: hash, ResourceKind: "incident", ResourceID: "incident-a"}
	_, err := BuildAuditOperationalReport(now, audit.Page{Entries: []audit.Entry{entry}}, []AuditReportBinding{binding})
	if err == nil || !strings.Contains(err.Error(), "future") {
		t.Fatalf("error = %v, want future-evidence rejection", err)
	}

	page := audit.Page{Entries: []audit.Entry{entry, entry}}
	_, err = BuildAuditOperationalReport(now.Add(2*time.Minute), page, []AuditReportBinding{binding, binding})
	if err == nil || !strings.Contains(err.Error(), "duplicate audit binding sequence_id") {
		t.Fatalf("error = %v, want duplicate-binding rejection", err)
	}
}
