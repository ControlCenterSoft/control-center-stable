package audit

import (
	"encoding/csv"
	"strings"
	"testing"
	"time"
)

func TestBuildCSVExportIsBoundedRedactedAndSpreadsheetSafe(t *testing.T) {
	first, err := Prepare(Event{
		ID:         "11111111-1111-1111-1111-111111111111",
		OccurredAt: time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC),
		Action:     "security.login",
		Outcome:    "success",
		ActorID:    "=SUM(1,1)",
		SubjectID:  "user-1",
		SourceIP:   "203.0.113.55",
		Details: map[string]any{
			"token":   "Bearer secret-value",
			"message": "safe",
			"network": map[string]any{"client_ip": "192.0.2.44"},
		},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Prepare(Event{
		ID:            "22222222-2222-2222-2222-222222222222",
		OccurredAt:    time.Date(2026, 9, 12, 18, 1, 0, 0, time.UTC),
		Action:        "change.approved",
		Outcome:       "success",
		ActorID:       "auditor-1",
		SubjectID:     "change-1",
		CorrelationID: "corr-1",
		SourceIP:      "198.51.100.10",
		Details:       map[string]any{"password": "do-not-export", "risk": "medium"},
	}, first.Hash)
	if err != nil {
		t.Fatal(err)
	}

	payload, manifest, err := BuildCSVExport([]Entry{
		{SequenceID: 2, Event: second},
		{SequenceID: 1, Event: first},
	}, time.Date(2026, 9, 12, 18, 2, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ContractVersion != CSVExportContractVersion || manifest.EventCount != 2 || manifest.NewestSequenceID != 2 || manifest.OldestSequenceID != 1 {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	if manifest.SourceIPIncluded || !manifest.SpreadsheetFormulaEscaping {
		t.Fatalf("unexpected privacy/export policy manifest: %#v", manifest)
	}
	text := string(payload)
	if strings.Contains(text, "203.0.113.55") || strings.Contains(text, "198.51.100.10") || strings.Contains(text, "192.0.2.44") {
		t.Fatalf("source IP leaked into export: %s", text)
	}
	if strings.Contains(text, "secret-value") || strings.Contains(text, "do-not-export") {
		t.Fatalf("sensitive details leaked into export: %s", text)
	}
	if !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("expected redaction marker in export: %s", text)
	}

	reader := csv.NewReader(strings.NewReader(text))
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows=%d", len(rows))
	}
	if rows[0][0] != "sequence_id" || rows[1][0] != "2" || rows[2][0] != "1" {
		t.Fatalf("unexpected order/header: %#v", rows)
	}
	if rows[2][5] != "'=SUM(1,1)" {
		t.Fatalf("spreadsheet formula was not neutralized: %q", rows[2][5])
	}
}

func TestBuildCSVExportRejectsInvalidBoundsAndOrdering(t *testing.T) {
	now := time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC)
	valid, err := Prepare(Event{ID: "33333333-3333-3333-3333-333333333333", OccurredAt: now, Action: "audit.test", Outcome: "success"}, "")
	if err != nil {
		t.Fatal(err)
	}

	tooMany := make([]Entry, CSVExportMaxEvents+1)
	for index := range tooMany {
		tooMany[index] = Entry{SequenceID: int64(CSVExportMaxEvents + 1 - index), Event: valid}
	}
	if _, _, err := BuildCSVExport(tooMany, now); err == nil {
		t.Fatal("expected bounded export rejection")
	}
	if _, _, err := BuildCSVExport([]Entry{{SequenceID: 1, Event: valid}, {SequenceID: 2, Event: valid}}, now); err == nil {
		t.Fatal("expected newest-first ordering rejection")
	}
	if _, _, err := BuildCSVExport([]Entry{{SequenceID: 1, Event: valid}, {SequenceID: 1, Event: valid}}, now); err == nil {
		t.Fatal("expected duplicate sequence rejection")
	}
	if _, _, err := BuildCSVExport([]Entry{{SequenceID: 1, Event: valid}}, time.Time{}); err == nil {
		t.Fatal("expected generated_at rejection")
	}
}

func TestBuildCSVExportRejectsMalformedAuditEvidence(t *testing.T) {
	now := time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC)
	malformed := Event{
		ID:         "event-1",
		OccurredAt: now,
		Action:     "audit.test",
		Outcome:    "success",
		Hash:       "not-a-sha256",
	}
	if _, _, err := BuildCSVExport([]Entry{{SequenceID: 1, Event: malformed}}, now); err == nil {
		t.Fatal("expected malformed hash rejection")
	}
}
