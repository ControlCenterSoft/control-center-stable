package audit

import (
	"context"
	"strings"
	"testing"
)

func TestMemoryLogInspectChainReportsVerifiedPrefix(t *testing.T) {
	log := NewMemoryLog()
	for _, event := range []Event{
		{Action: "identity.login", Outcome: "success"},
		{Action: "authorization.check", Outcome: "denied"},
	} {
		if err := log.Append(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}

	report, err := log.InspectChain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	records := log.Records()
	if report.EventsChecked != len(records) {
		t.Fatalf("events_checked=%d want=%d", report.EventsChecked, len(records))
	}
	if report.HeadHash == "" || report.HeadHash != records[len(records)-1].Hash {
		t.Fatalf("head_hash=%q want=%q", report.HeadHash, records[len(records)-1].Hash)
	}
}

func TestMemoryLogInspectChainFailsClosedOnTamper(t *testing.T) {
	log := NewMemoryLog()
	if err := log.Append(context.Background(), Event{Action: "identity.login", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(context.Background(), Event{Action: "identity.logout", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}

	log.mu.Lock()
	log.records[0].Outcome = "tampered"
	log.mu.Unlock()

	_, err := log.InspectChain(context.Background())
	if err == nil || !strings.Contains(err.Error(), "offset 0") {
		t.Fatalf("InspectChain() error=%v, want tamper failure at offset 0", err)
	}
}

func TestMemoryLogInspectChainHonorsCancelledContext(t *testing.T) {
	log := NewMemoryLog()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := log.InspectChain(ctx); err == nil {
		t.Fatal("InspectChain() accepted cancelled context")
	}
}
