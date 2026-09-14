package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"control-center/internal/corecontracts"
	"control-center/internal/incidents"
)

func TestPostgresIncidentReadRepositoryCreateListAndCASReplace(t *testing.T) {
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

	var tablePresent bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('cc_incident_read_models') IS NOT NULL`).Scan(&tablePresent); err != nil || !tablePresent {
		t.Fatalf("0013 incident migration is required: present=%v err=%v", tablePresent, err)
	}

	repository, err := NewIncidentReadRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	suffix := fmt.Sprintf("%d", now.UnixNano())
	incident := persistenceIncident(suffix, now)
	defer func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM cc_incident_read_models WHERE object_id=$1`, incident.ObjectID)
	}()

	if err := repository.Create(ctx, incident); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := repository.Create(ctx, incident); !errors.Is(err, ErrIncidentConflict) {
		t.Fatalf("duplicate Create() error = %v, want ErrIncidentConflict", err)
	}

	stored, err := repository.Get(ctx, incident.ObjectID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.ObjectID != incident.ObjectID || stored.ResourceVersion != incident.ResourceVersion || stored.Title != incident.Title {
		t.Fatalf("Get() = %#v", stored)
	}

	page, err := repository.List(ctx, incidents.ListQuery{
		Limit:         10,
		ScopeID:       incident.ScopeID,
		ResourceKind:  "node",
		ResourceID:    "node-" + suffix,
		Statuses:      []incidents.Status{incidents.StatusOpen},
		Severities:    []incidents.Severity{incidents.SeverityWarning},
		StartedFrom:   timePointer(now.Add(-time.Second)),
		StartedBefore: timePointer(now.Add(time.Second)),
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ObjectID != incident.ObjectID || page.HasMore || page.Next != nil {
		t.Fatalf("List() page = %#v", page)
	}

	next := incident
	next.ResourceVersion = "rv-" + suffix + "-2"
	next.UpdatedAt = incident.UpdatedAt.Add(time.Minute)
	next.LastObservedAt = incident.LastObservedAt.Add(time.Minute)
	next.Severity = incidents.SeverityCritical
	next.Signals = append(append([]incidents.Signal(nil), incident.Signals...), incidents.Signal{
		ID:         "signal-" + suffix + "-2",
		Kind:       "health-degraded",
		Source:     "core-health",
		ObservedAt: next.LastObservedAt,
		Summary:    "Повторный сигнал с повышением критичности",
	})
	next.Timeline = append(append([]incidents.TimelineEntry(nil), incident.Timeline...), incidents.TimelineEntry{
		Kind:    incidents.TimelineSignalObserved,
		At:      next.LastObservedAt,
		Summary: "Получен новый сигнал",
	})
	next.AffectedResources = append(append([]incidents.ResourceRef(nil), incident.AffectedResources...), incidents.ResourceRef{
		Kind:    "node",
		ID:      "node-extra-" + suffix,
		ScopeID: incident.ScopeID,
	})
	if err := next.Validate(); err != nil {
		t.Fatalf("next fixture invalid: %v", err)
	}

	wrong := corecontracts.ObjectPrecondition{ObjectID: incident.ObjectID, ResourceVersion: "rv-stale"}
	if err := repository.Replace(ctx, wrong, next, false); !errors.Is(err, corecontracts.ErrPreconditionFailed) {
		t.Fatalf("stale Replace() error = %v, want ErrPreconditionFailed", err)
	}
	unchanged, err := repository.Get(ctx, incident.ObjectID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.ResourceVersion != incident.ResourceVersion || unchanged.Severity != incident.Severity {
		t.Fatalf("stale replace mutated incident: %#v", unchanged)
	}

	precondition := corecontracts.ObjectPrecondition{ObjectID: incident.ObjectID, ResourceVersion: incident.ResourceVersion}
	if err := repository.Replace(ctx, precondition, next, false); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	updated, err := repository.Get(ctx, incident.ObjectID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ResourceVersion != next.ResourceVersion || updated.Severity != incidents.SeverityCritical || len(updated.AffectedResources) != 2 {
		t.Fatalf("updated incident = %#v", updated)
	}

	page, err = repository.List(ctx, incidents.ListQuery{
		ScopeID:      incident.ScopeID,
		ResourceKind: "node",
		ResourceID:   "node-extra-" + suffix,
	})
	if err != nil {
		t.Fatalf("List(updated resource) error = %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ObjectID != incident.ObjectID {
		t.Fatalf("updated resource index page = %#v", page)
	}
}

func persistenceIncident(suffix string, started time.Time) incidents.Incident {
	observed := started.Add(100 * time.Millisecond)
	objectID := "incident:" + suffix
	scopeID := "site:primary"
	return incidents.Incident{
		ObjectMetadata: corecontracts.ObjectMetadata{
			ObjectID:        objectID,
			ScopeID:         scopeID,
			OwnerScope:      "installation:default",
			Generation:      1,
			ResourceVersion: "rv-" + suffix + "-1",
			CreatedAt:       started,
			UpdatedAt:       observed,
		},
		Severity:       incidents.SeverityWarning,
		Status:         incidents.StatusOpen,
		Title:          "Проверка PostgreSQL incident read model",
		StartedAt:      started,
		LastObservedAt: observed,
		AffectedResources: []incidents.ResourceRef{
			{Kind: "node", ID: "node-" + suffix, ScopeID: scopeID},
		},
		Signals: []incidents.Signal{
			{
				ID:         "signal-" + suffix + "-1",
				Kind:       "health-degraded",
				Source:     "core-health",
				ObservedAt: observed,
				Summary:    "Зафиксирован тестовый сигнал деградации",
			},
		},
		Timeline: []incidents.TimelineEntry{
			{Kind: incidents.TimelineOpened, At: started},
			{Kind: incidents.TimelineSignalObserved, At: observed, Summary: "Зафиксирован тестовый сигнал"},
		},
	}
}

func timePointer(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}
