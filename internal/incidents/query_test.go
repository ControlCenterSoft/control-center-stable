package incidents

import (
	"reflect"
	"testing"
	"time"
)

func TestNormalizeListQueryDefaultsAndCanonicalizes(t *testing.T) {
	query, err := NormalizeListQuery(ListQuery{
		Statuses:   []Status{StatusResolved, StatusOpen},
		Severities: []Severity{SeverityWarning, SeverityCritical},
	})
	if err != nil {
		t.Fatalf("NormalizeListQuery() error = %v", err)
	}
	if query.Limit != DefaultListLimit {
		t.Fatalf("Limit = %d, want %d", query.Limit, DefaultListLimit)
	}
	if !reflect.DeepEqual(query.Statuses, []Status{StatusOpen, StatusResolved}) {
		t.Fatalf("Statuses = %#v", query.Statuses)
	}
	if !reflect.DeepEqual(query.Severities, []Severity{SeverityCritical, SeverityWarning}) {
		t.Fatalf("Severities = %#v", query.Severities)
	}
}

func TestNormalizeListQueryRejectsAmbiguousFilters(t *testing.T) {
	tests := []ListQuery{
		{Limit: MaxListLimit + 1},
		{Statuses: []Status{StatusOpen, StatusOpen}},
		{Severities: []Severity{"emergency"}},
		{ResourceID: "node-1"},
		{Before: &ListCursor{StartedAt: time.Now().UTC(), ObjectID: "bad id"}},
	}
	for idx, query := range tests {
		if _, err := NormalizeListQuery(query); err == nil {
			t.Fatalf("case %d: expected error", idx)
		}
	}
}

func TestNormalizeListQueryRejectsInvertedWindow(t *testing.T) {
	to := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	from := to.Add(time.Minute)
	if _, err := NormalizeListQuery(ListQuery{StartedFrom: &from, StartedBefore: &to}); err == nil {
		t.Fatal("expected inverted time window error")
	}
}

func TestFilterPageUsesStableKeysetPagination(t *testing.T) {
	base := validIncident(StatusOpen)
	base.ObjectID = "incident:b"
	base.ResourceVersion = "rv-b"

	sameTime := validIncident(StatusOpen)
	sameTime.ObjectID = "incident:a"
	sameTime.ResourceVersion = "rv-a"

	older := validIncident(StatusOpen)
	older.ObjectID = "incident:c"
	older.ResourceVersion = "rv-c"
	older.CreatedAt = older.CreatedAt.Add(-time.Hour)
	older.UpdatedAt = older.UpdatedAt.Add(-time.Hour)
	older.StartedAt = older.StartedAt.Add(-time.Hour)
	older.LastObservedAt = older.LastObservedAt.Add(-time.Hour)
	for idx := range older.Signals {
		older.Signals[idx].ObservedAt = older.Signals[idx].ObservedAt.Add(-time.Hour)
		for evidence := range older.Signals[idx].Evidence {
			older.Signals[idx].Evidence[evidence].Collected = older.Signals[idx].Evidence[evidence].Collected.Add(-time.Hour)
		}
	}
	for idx := range older.Evidence {
		older.Evidence[idx].Collected = older.Evidence[idx].Collected.Add(-time.Hour)
	}
	for idx := range older.Timeline {
		older.Timeline[idx].At = older.Timeline[idx].At.Add(-time.Hour)
		for evidence := range older.Timeline[idx].Evidence {
			older.Timeline[idx].Evidence[evidence].Collected = older.Timeline[idx].Evidence[evidence].Collected.Add(-time.Hour)
		}
	}

	first, err := FilterPage([]Incident{base, older, sameTime}, ListQuery{Limit: 2})
	if err != nil {
		t.Fatalf("FilterPage(first) error = %v", err)
	}
	if !first.HasMore || first.Next == nil {
		t.Fatalf("first page = %#v, want continuation", first)
	}
	if got := []string{first.Items[0].ObjectID, first.Items[1].ObjectID}; !reflect.DeepEqual(got, []string{"incident:a", "incident:b"}) {
		t.Fatalf("first ids = %#v", got)
	}

	second, err := FilterPage([]Incident{base, older, sameTime}, ListQuery{Limit: 2, Before: first.Next})
	if err != nil {
		t.Fatalf("FilterPage(second) error = %v", err)
	}
	if second.HasMore || second.Next != nil || len(second.Items) != 1 || second.Items[0].ObjectID != "incident:c" {
		t.Fatalf("second page = %#v", second)
	}
}

func TestFilterPageAppliesScopeAndResourceFilters(t *testing.T) {
	matching := validIncident(StatusAcknowledged)
	other := validIncident(StatusAcknowledged)
	other.ObjectID = "incident:other"
	other.ResourceVersion = "rv-other"
	other.ScopeID = "site:secondary"
	other.AffectedResources[0] = ResourceRef{Kind: "node", ID: "node-2", ScopeID: "site:secondary"}

	page, err := FilterPage([]Incident{other, matching}, ListQuery{
		ScopeID:      "site:primary",
		ResourceKind: "node",
		ResourceID:   "node-1",
		Statuses:     []Status{StatusAcknowledged},
	})
	if err != nil {
		t.Fatalf("FilterPage() error = %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ObjectID != matching.ObjectID {
		t.Fatalf("page = %#v", page)
	}
}

func TestFilterPageFailsClosedOnMalformedStoredIncident(t *testing.T) {
	incident := validIncident(StatusOpen)
	incident.Title = ""
	if _, err := FilterPage([]Incident{incident}, ListQuery{}); err == nil {
		t.Fatal("expected invalid stored incident error")
	}
}
