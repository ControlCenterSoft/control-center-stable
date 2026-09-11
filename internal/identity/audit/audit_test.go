package audit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAuditRedactionAndChain(t *testing.T) {
	log := NewMemoryLog()
	if err := log.Append(context.Background(), Event{Action: "auth.login", Outcome: "denied", Details: map[string]any{"password": "synthetic-password", "nested": map[string]any{"api_key": "synthetic-key"}, "header": "Bearer synthetic-token"}}); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(context.Background(), Event{Action: "auth.login", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	records := log.Records()
	serialized, _ := jsonMarshal(records)
	if strings.Contains(serialized, "synthetic-password") || strings.Contains(serialized, "synthetic-key") || strings.Contains(serialized, "synthetic-token") {
		t.Fatalf("secret leaked in audit: %s", serialized)
	}
	if records[1].PreviousHash != records[0].Hash || records[1].PreviousHash == "" {
		t.Fatal("audit chain was not linked")
	}
}
func TestPrepareCanonicalizesTimestampBeforeHash(t *testing.T) {
	inputTime := time.Date(2026, 9, 8, 12, 34, 56, 123456789, time.FixedZone("test", 3*60*60))
	prepared, err := Prepare(Event{ID: "event-1", OccurredAt: inputTime, Action: "identity.login", Outcome: "success"}, "previous-hash")
	if err != nil {
		t.Fatal(err)
	}
	wantTime := inputTime.UTC().Truncate(time.Microsecond)
	if prepared.OccurredAt != wantTime {
		t.Fatalf("canonical timestamp = %s, want %s", prepared.OccurredAt, wantTime)
	}
	if err := Verify(prepared, "previous-hash"); err != nil {
		t.Fatalf("prepared event did not verify: %v", err)
	}
	postgresRoundTrip := prepared
	postgresRoundTrip.OccurredAt = time.UnixMicro(prepared.OccurredAt.UnixMicro()).UTC()
	if err := Verify(postgresRoundTrip, "previous-hash"); err != nil {
		t.Fatalf("PostgreSQL-microsecond round trip changed hash: %v", err)
	}
}
func TestVerifyRejectsBrokenAuditChain(t *testing.T) {
	first, err := Prepare(Event{ID: "event-1", OccurredAt: time.Now(), Action: "first", Outcome: "success"}, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Prepare(Event{ID: "event-2", OccurredAt: time.Now(), Action: "second", Outcome: "success"}, first.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(second, first.Hash); err != nil {
		t.Fatal(err)
	}
	if err := Verify(second, "wrong-predecessor"); err == nil {
		t.Fatal("Verify accepted a broken predecessor link")
	}
	second.Outcome = "tampered"
	if err := Verify(second, first.Hash); err == nil {
		t.Fatal("Verify accepted tampered event content")
	}
}
func jsonMarshal(value any) (string, error) { b, err := json.Marshal(value); return string(b), err }
