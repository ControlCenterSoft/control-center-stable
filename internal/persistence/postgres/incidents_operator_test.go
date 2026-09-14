package postgres

import (
	"testing"
	"time"

	"control-center/internal/corecontracts"
	"control-center/internal/incidents"
)

func TestIncidentOperatorAuditEventBindsExactMutationRevision(t *testing.T) {
	now := time.Date(2026, 9, 12, 19, 45, 0, 123456000, time.UTC)
	current := incidents.Incident{
		ObjectMetadata: corecontracts.ObjectMetadata{
			ObjectID:        "incident-1",
			ScopeID:         "site-1",
			OwnerScope:      "site-1",
			Generation:      7,
			ResourceVersion: "rv-7",
		},
		Status: incidents.StatusOpen,
	}
	next := current
	next.Status = incidents.StatusAcknowledged
	next.Generation = 8
	next.ResourceVersion = "rv-8"
	record := incidents.OperatorAuditRecord{
		Action:                incidents.CapabilityIncidentAcknowledge,
		ActorID:               "11111111-1111-4111-8111-111111111111",
		IncidentID:            "incident-1",
		ScopeID:               "site-1",
		OccurredAt:            now,
		BeforeStatus:          incidents.StatusOpen,
		AfterStatus:           incidents.StatusAcknowledged,
		BeforeGeneration:      7,
		AfterGeneration:       8,
		BeforeResourceVersion: "rv-7",
		AfterResourceVersion:  "rv-8",
	}

	event, err := incidentOperatorAuditEvent(current, next, record)
	if err != nil {
		t.Fatalf("incidentOperatorAuditEvent() error = %v", err)
	}
	if event.Action != string(incidents.CapabilityIncidentAcknowledge) || event.Outcome != "success" {
		t.Fatalf("unexpected audit identity: %#v", event)
	}
	if event.ActorID != record.ActorID || event.SubjectID != record.IncidentID || !event.OccurredAt.Equal(now) {
		t.Fatalf("unexpected audit principals/time: %#v", event)
	}
	if event.Details["before_resource_version"] != "rv-7" || event.Details["after_resource_version"] != "rv-8" {
		t.Fatalf("audit event is not bound to exact resource versions: %#v", event.Details)
	}
}

func TestIncidentOperatorAuditEventRejectsRevisionMismatch(t *testing.T) {
	current := incidents.Incident{
		ObjectMetadata: corecontracts.ObjectMetadata{
			ObjectID:        "incident-1",
			ScopeID:         "site-1",
			Generation:      2,
			ResourceVersion: "rv-2",
		},
		Status: incidents.StatusOpen,
	}
	next := current
	next.Status = incidents.StatusAcknowledged
	next.Generation = 3
	next.ResourceVersion = "rv-3"
	record := incidents.OperatorAuditRecord{
		Action:                incidents.CapabilityIncidentAcknowledge,
		ActorID:               "11111111-1111-4111-8111-111111111111",
		IncidentID:            "incident-1",
		ScopeID:               "site-1",
		OccurredAt:            time.Now().UTC(),
		BeforeStatus:          incidents.StatusOpen,
		AfterStatus:           incidents.StatusAcknowledged,
		BeforeGeneration:      2,
		AfterGeneration:       3,
		BeforeResourceVersion: "rv-stale",
		AfterResourceVersion:  "rv-3",
	}

	if _, err := incidentOperatorAuditEvent(current, next, record); err == nil {
		t.Fatal("expected exact revision binding mismatch to fail closed")
	}
}

func TestIncidentOperatorAuditEventRejectsUnsupportedAction(t *testing.T) {
	current := incidents.Incident{
		ObjectMetadata: corecontracts.ObjectMetadata{
			ObjectID:        "incident-1",
			ScopeID:         "site-1",
			Generation:      1,
			ResourceVersion: "rv-1",
		},
		Status: incidents.StatusOpen,
	}
	next := current
	next.Generation = 2
	next.ResourceVersion = "rv-2"
	record := incidents.OperatorAuditRecord{
		Action:                incidents.OperatorCapability("incidents.delete"),
		ActorID:               "11111111-1111-4111-8111-111111111111",
		IncidentID:            "incident-1",
		ScopeID:               "site-1",
		OccurredAt:            time.Now().UTC(),
		BeforeStatus:          incidents.StatusOpen,
		AfterStatus:           incidents.StatusOpen,
		BeforeGeneration:      1,
		AfterGeneration:       2,
		BeforeResourceVersion: "rv-1",
		AfterResourceVersion:  "rv-2",
	}

	if _, err := incidentOperatorAuditEvent(current, next, record); err == nil {
		t.Fatal("expected unsupported audit action to fail closed")
	}
}
