package audit

import (
	"context"
	"encoding/hex"
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

func TestPrepareGeneratesPostgresCanonicalUUIDBeforeHash(t *testing.T) {
	prepared, err := Prepare(Event{Action: "audit.test", Outcome: "success"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.ID) != 36 || prepared.ID[8] != '-' || prepared.ID[13] != '-' || prepared.ID[18] != '-' || prepared.ID[23] != '-' {
		t.Fatalf("generated audit id is not canonical UUID text: %q", prepared.ID)
	}
	compact := strings.ReplaceAll(prepared.ID, "-", "")
	if _, err := hex.DecodeString(compact); err != nil {
		t.Fatalf("generated audit id is not hexadecimal UUID text: %q: %v", prepared.ID, err)
	}
	if err := Verify(prepared, ""); err != nil {
		t.Fatalf("canonical generated audit event did not verify: %v", err)
	}
}

func TestVerifyAcceptsLegacyPostgresUUIDRepresentation(t *testing.T) {
	legacy := Event{
		ID:           "00112233445566778899aabbccddeeff",
		OccurredAt:   time.Date(2026, 9, 10, 12, 0, 0, 123456000, time.UTC),
		Action:       "legacy.audit",
		Outcome:      "success",
		PreviousHash: "previous",
	}
	legacy.Hash = hashEvent(legacy)
	legacy.ID = "00112233-4455-6677-8899-aabbccddeeff"
	if err := Verify(legacy, "previous"); err != nil {
		t.Fatalf("legacy PostgreSQL UUID text did not verify: %v", err)
	}
	legacy.Outcome = "tampered"
	if err := Verify(legacy, "previous"); err == nil {
		t.Fatal("legacy compatibility path accepted tampered event content")
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

func jsonMarshal(value any) (string, error) {
	b, err := json.Marshal(value)
	return string(b), err
}
