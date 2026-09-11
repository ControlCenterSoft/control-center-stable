package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"control-center/internal/identity/audit"
)

func TestPostgresAuditIntegrityReportsVerifiedPrefix(t *testing.T) {
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
	action := fmt.Sprintf("integration.audit-integrity.%d", time.Now().UnixNano())
	if err := log.Append(ctx, audit.Event{Action: action, Outcome: "success"}); err != nil {
		t.Fatal(err)
	}

	page, err := log.Read(ctx, audit.Query{Limit: 1, Action: action, Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 {
		t.Fatalf("audit read entries=%d want=1", len(page.Entries))
	}

	report, err := log.InspectChain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.EventsChecked < 1 {
		t.Fatalf("events_checked=%d want>=1", report.EventsChecked)
	}
	if report.HeadHash == "" {
		t.Fatal("verified prefix returned an empty head hash for a non-empty audit chain")
	}

	if err := audit.Verify(page.Entries[0].Event, page.Entries[0].Event.PreviousHash); err != nil {
		t.Fatalf("stored event is not independently verifiable: %v", err)
	}
}

func TestPostgresAuditIntegrityHonorsCancelledContext(t *testing.T) {
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

	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := log.InspectChain(cancelled); err == nil {
		t.Fatal("InspectChain accepted a cancelled context")
	}
}
