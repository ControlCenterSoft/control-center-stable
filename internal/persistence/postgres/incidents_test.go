package postgres

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"control-center/internal/corecontracts"
	"control-center/internal/incidents"
)

func TestBuildIncidentListSQLUsesBoundedKeysetQuery(t *testing.T) {
	from := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	before := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	cursorAt := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	query, err := incidents.NormalizeListQuery(incidents.ListQuery{
		Limit:         25,
		Statuses:      []incidents.Status{incidents.StatusResolved, incidents.StatusOpen},
		Severities:    []incidents.Severity{incidents.SeverityCritical},
		ScopeID:       "site:primary",
		ResourceKind:  "node",
		ResourceID:    "node-1",
		StartedFrom:   &from,
		StartedBefore: &before,
		Before:        &incidents.ListCursor{StartedAt: cursorAt, ObjectID: "incident:42"},
	})
	if err != nil {
		t.Fatalf("NormalizeListQuery() error = %v", err)
	}

	statement, args := buildIncidentListSQL(query)
	for _, want := range []string{
		"scope_id=$1",
		"status IN ($2,$3)",
		"severity IN ($4)",
		"started_at>=$5",
		"started_at<$6",
		"(started_at<$7 OR (started_at=$7 AND object_id>$8))",
		"r.resource_kind=$9",
		"r.resource_id=$10",
		"ORDER BY started_at DESC, object_id ASC LIMIT $11",
	} {
		if !strings.Contains(statement, want) {
			t.Fatalf("statement missing %q:\n%s", want, statement)
		}
	}
	if len(args) != 11 {
		t.Fatalf("args len = %d, want 11: %#v", len(args), args)
	}
	if args[0] != "site:primary" || args[1] != string(incidents.StatusOpen) || args[2] != string(incidents.StatusResolved) {
		t.Fatalf("unexpected canonical leading args: %#v", args[:3])
	}
	if args[10] != 26 {
		t.Fatalf("limit arg = %#v, want 26", args[10])
	}
}

func TestBuildIncidentListSQLDoesNotMutateNormalizedQuery(t *testing.T) {
	query, err := incidents.NormalizeListQuery(incidents.ListQuery{
		Statuses: []incidents.Status{incidents.StatusAcknowledged},
	})
	if err != nil {
		t.Fatalf("NormalizeListQuery() error = %v", err)
	}
	before := query
	_, _ = buildIncidentListSQL(query)
	if !reflect.DeepEqual(query, before) {
		t.Fatalf("query mutated:\n got %#v\nwant %#v", query, before)
	}
}

func TestCanonicalizeIncidentTimesMatchesPostgresPrecision(t *testing.T) {
	location := time.FixedZone("source", 3*60*60)
	created := time.Date(2026, 9, 12, 14, 0, 0, 123456789, location)
	updated := created.Add(3*time.Second + 444*time.Nanosecond)
	started := created.Add(time.Second + 222*time.Nanosecond)
	observed := created.Add(2*time.Second + 333*time.Nanosecond)
	incident := incidents.Incident{
		ObjectMetadata: corecontracts.ObjectMetadata{
			CreatedAt: created,
			UpdatedAt: updated,
		},
		StartedAt:      started,
		LastObservedAt: observed,
		Acknowledgement: &incidents.Acknowledgement{
			At: updated,
		},
		Signals: []incidents.Signal{{
			ObservedAt: observed,
			Evidence:   []incidents.EvidenceRef{{Collected: observed}},
		}},
		Timeline: []incidents.TimelineEntry{{
			At:       updated,
			Evidence: []incidents.EvidenceRef{{Collected: updated}},
		}},
		Evidence: []incidents.EvidenceRef{{Collected: observed}},
	}

	canonicalizeIncidentTimes(&incident)
	values := map[string]time.Time{
		"created_at":                incident.CreatedAt,
		"updated_at":                incident.UpdatedAt,
		"started_at":                incident.StartedAt,
		"last_observed_at":          incident.LastObservedAt,
		"acknowledgement.at":        incident.Acknowledgement.At,
		"signal.observed_at":        incident.Signals[0].ObservedAt,
		"signal.evidence.collected": incident.Signals[0].Evidence[0].Collected,
		"timeline.at":               incident.Timeline[0].At,
		"timeline.evidence":         incident.Timeline[0].Evidence[0].Collected,
		"evidence.collected":        incident.Evidence[0].Collected,
	}
	for name, got := range values {
		if got.Location() != time.UTC {
			t.Fatalf("%s location = %v, want UTC", name, got.Location())
		}
		if got.Nanosecond()%1000 != 0 {
			t.Fatalf("%s nanoseconds = %d, want microsecond precision", name, got.Nanosecond())
		}
	}
	wantCreated := created.UTC().Truncate(time.Microsecond)
	if !incident.CreatedAt.Equal(wantCreated) {
		t.Fatalf("created_at = %s, want %s", incident.CreatedAt, wantCreated)
	}
}
