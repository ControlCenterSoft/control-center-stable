package audit

import (
	"context"
	"testing"
)

func TestMemoryLogReadIsNewestFirstAndCursorBounded(t *testing.T) {
	log := NewMemoryLog()
	for index := 1; index <= 4; index++ {
		if err := log.Append(context.Background(), Event{
			Action:    "identity.test",
			Outcome:   "success",
			ActorID:   "actor-1",
			SubjectID: "subject-1",
			Details:   map[string]any{"index": index},
		}); err != nil {
			t.Fatal(err)
		}
	}

	first, err := log.Read(context.Background(), Query{Limit: 2, Action: "identity.test"})
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasMore || len(first.Entries) != 2 {
		t.Fatalf("first page has_more=%v len=%d", first.HasMore, len(first.Entries))
	}
	if first.Entries[0].SequenceID != 4 || first.Entries[1].SequenceID != 3 {
		t.Fatalf("unexpected first page sequence: %#v", first.Entries)
	}

	second, err := log.Read(context.Background(), Query{
		Limit:            2,
		BeforeSequenceID: first.Entries[len(first.Entries)-1].SequenceID,
		Action:           "identity.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.HasMore || len(second.Entries) != 2 {
		t.Fatalf("second page has_more=%v len=%d", second.HasMore, len(second.Entries))
	}
	if second.Entries[0].SequenceID != 2 || second.Entries[1].SequenceID != 1 {
		t.Fatalf("unexpected second page sequence: %#v", second.Entries)
	}
}

func TestMemoryLogReadFiltersExactlyAndReturnsDefensiveDetails(t *testing.T) {
	log := NewMemoryLog()
	for _, event := range []Event{
		{Action: "identity.login", Outcome: "success", ActorID: "actor-a", SubjectID: "subject-a", Details: map[string]any{"state": "original"}},
		{Action: "identity.login", Outcome: "denied", ActorID: "actor-a", SubjectID: "subject-b"},
		{Action: "identity.password", Outcome: "success", ActorID: "actor-b", SubjectID: "subject-a"},
	} {
		if err := log.Append(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}

	page, err := log.Read(context.Background(), Query{
		Action: "identity.login", Outcome: "success", ActorID: "actor-a", SubjectID: "subject-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.HasMore || len(page.Entries) != 1 {
		t.Fatalf("filtered page has_more=%v len=%d", page.HasMore, len(page.Entries))
	}
	page.Entries[0].Event.Details["state"] = "mutated"

	again, err := log.Read(context.Background(), Query{Action: "identity.login", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Entries[0].Event.Details["state"]; got != "original" {
		t.Fatalf("reader exposed mutable audit details: %v", got)
	}
}

func TestNormalizeQueryRejectsUnboundedInputs(t *testing.T) {
	for _, query := range []Query{
		{Limit: MaxReadLimit + 1},
		{Limit: -1},
		{BeforeSequenceID: -1},
		{Action: string(make([]byte, 193))},
	} {
		if _, err := NormalizeQuery(query); err == nil {
			t.Fatalf("NormalizeQuery accepted invalid query: %#v", query)
		}
	}

	normalized, err := NormalizeQuery(Query{})
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Limit != DefaultReadLimit {
		t.Fatalf("default limit=%d want=%d", normalized.Limit, DefaultReadLimit)
	}
}

func TestMemoryLogReadHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewMemoryLog().Read(ctx, Query{}); err == nil {
		t.Fatal("Read accepted a cancelled context")
	}
}
