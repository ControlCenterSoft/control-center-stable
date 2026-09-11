package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"control-center/internal/identity/audit"
)

func TestPostgresAuditReadPaginationAndExactFilters(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set; PostgreSQL integration test skipped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	log, err := NewAuditLog(db)
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	action := "integration.audit-read." + suffix
	subject := "subject-" + suffix
	for index := 1; index <= 3; index++ {
		if err := log.Append(ctx, audit.Event{
			Action: action, Outcome: "success", SubjectID: subject,
			Details: map[string]any{"index": index, "token": "must-not-leak"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := log.Append(ctx, audit.Event{Action: action, Outcome: "denied", SubjectID: subject}); err != nil {
		t.Fatal(err)
	}

	first, err := log.Read(ctx, audit.Query{Limit: 2, Action: action, Outcome: "success", SubjectID: subject})
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasMore || len(first.Entries) != 2 {
		t.Fatalf("first page has_more=%v len=%d", first.HasMore, len(first.Entries))
	}
	if first.Entries[0].Event.Details["index"] != float64(3) || first.Entries[1].Event.Details["index"] != float64(2) {
		t.Fatalf("unexpected first page: %#v", first.Entries)
	}
	for _, entry := range first.Entries {
		if entry.Event.Details["token"] != "[REDACTED]" {
			t.Fatalf("stored sensitive detail was not redacted: %#v", entry.Event.Details)
		}
	}

	second, err := log.Read(ctx, audit.Query{
		Limit:            2,
		BeforeSequenceID: first.Entries[len(first.Entries)-1].SequenceID,
		Action:           action,
		Outcome:          "success",
		SubjectID:        subject,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.HasMore || len(second.Entries) != 1 || second.Entries[0].Event.Details["index"] != float64(1) {
		t.Fatalf("unexpected second page: %#v", second)
	}
}
