package incidents

import (
	"errors"
	"strings"
	"testing"
	"time"

	"control-center/internal/corecontracts"
)

func TestIncidentValidateAcknowledged(t *testing.T) {
	incident := validIncident(StatusAcknowledged)
	if err := incident.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestIncidentRejectsFalseResolved(t *testing.T) {
	incident := validIncident(StatusResolved)
	incident.Timeline = incident.Timeline[:len(incident.Timeline)-1]
	if err := incident.Validate(); !errors.Is(err, ErrInvalidTimeline) {
		t.Fatalf("Validate() error = %v, want ErrInvalidTimeline", err)
	}
}

func TestIncidentRejectsDuplicateAffectedResource(t *testing.T) {
	incident := validIncident(StatusAcknowledged)
	incident.AffectedResources = append(incident.AffectedResources, incident.AffectedResources[0])
	if err := incident.Validate(); !errors.Is(err, ErrInvalidIncident) {
		t.Fatalf("Validate() error = %v, want ErrInvalidIncident", err)
	}
}

func TestIncidentRejectsDuplicateSignalID(t *testing.T) {
	incident := validIncident(StatusAcknowledged)
	duplicate := incident.Signals[0]
	duplicate.ObservedAt = incident.LastObservedAt
	incident.Signals = append(incident.Signals, duplicate)
	if err := incident.Validate(); !errors.Is(err, ErrInvalidIncident) {
		t.Fatalf("Validate() error = %v, want ErrInvalidIncident", err)
	}
}

func TestIncidentRejectsNewestSignalMismatch(t *testing.T) {
	incident := validIncident(StatusAcknowledged)
	incident.LastObservedAt = incident.LastObservedAt.Add(time.Minute)
	incident.UpdatedAt = incident.LastObservedAt
	if err := incident.Validate(); !errors.Is(err, ErrInvalidIncident) {
		t.Fatalf("Validate() error = %v, want ErrInvalidIncident", err)
	}
}

func TestIncidentRejectsDuplicateEvidence(t *testing.T) {
	incident := validIncident(StatusAcknowledged)
	incident.Evidence = append(incident.Evidence, incident.Evidence[0])
	if err := incident.Validate(); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("Validate() error = %v, want ErrInvalidEvidence", err)
	}
}

func TestIncidentRejectsInvalidEvidenceDigest(t *testing.T) {
	incident := validIncident(StatusAcknowledged)
	incident.Evidence[0].SHA256 = strings.Repeat("A", 64)
	if err := incident.Validate(); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("Validate() error = %v, want ErrInvalidEvidence", err)
	}
}

func TestOpenIncidentRejectsAcknowledgementState(t *testing.T) {
	incident := validIncident(StatusOpen)
	incident.Acknowledgement = &Acknowledgement{ActorID: "user:admin", At: incident.StartedAt.Add(time.Minute)}
	if err := incident.Validate(); !errors.Is(err, ErrInvalidAcknowledgement) {
		t.Fatalf("Validate() error = %v, want ErrInvalidAcknowledgement", err)
	}
}

func TestResolvedIncidentRequiresAcknowledgement(t *testing.T) {
	incident := validIncident(StatusResolved)
	incident.Acknowledgement = nil
	if err := incident.Validate(); !errors.Is(err, ErrInvalidAcknowledgement) {
		t.Fatalf("Validate() error = %v, want ErrInvalidAcknowledgement", err)
	}
}

func TestTimelineRejectsOperatorOnSignalObservation(t *testing.T) {
	incident := validIncident(StatusAcknowledged)
	incident.Timeline[1].ActorID = "user:admin"
	if err := incident.Validate(); !errors.Is(err, ErrInvalidTimeline) {
		t.Fatalf("Validate() error = %v, want ErrInvalidTimeline", err)
	}
}

func TestSortTimelineDeterministic(t *testing.T) {
	t0 := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	entries := []TimelineEntry{
		{Kind: TimelineResolved, At: t0.Add(time.Minute)},
		{Kind: TimelineSignalObserved, At: t0},
		{Kind: TimelineOpened, At: t0},
	}
	got := SortTimeline(entries)
	if got[0].Kind != TimelineOpened || got[1].Kind != TimelineSignalObserved || got[2].Kind != TimelineResolved {
		t.Fatalf("SortTimeline() = %#v", got)
	}
	if entries[0].Kind != TimelineResolved {
		t.Fatal("SortTimeline mutated input")
	}
}

func validIncident(status Status) Incident {
	started := time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)
	observed := started.Add(2 * time.Minute)
	ackAt := started.Add(3 * time.Minute)
	updated := ackAt
	metadata := corecontracts.ObjectMetadata{
		ObjectID:        "incident:inc-1",
		ScopeID:         "site:primary",
		OwnerScope:      "installation:default",
		Generation:      2,
		ResourceVersion: "rv-2",
		CreatedAt:       started,
		UpdatedAt:       updated,
	}
	evidence := EvidenceRef{
		Kind:      "audit-event",
		ID:        "audit:evt-1",
		SHA256:    strings.Repeat("a", 64),
		Collected: observed,
		Redaction: RedactionApplied,
	}
	incident := Incident{
		ObjectMetadata: metadata,
		Severity:       SeverityWarning,
		Status:         status,
		Title:          "Потеря свежести сетевой проверки",
		StartedAt:      started,
		LastObservedAt: observed,
		AffectedResources: []ResourceRef{
			{Kind: "node", ID: "node-1", ScopeID: "site:primary"},
		},
		Signals: []Signal{
			{ID: "signal-1", Kind: "network-verification-stale", Source: "core-network", ObservedAt: observed, Summary: "Последняя connectivity verification вышла за freshness budget", Evidence: []EvidenceRef{evidence}},
		},
		Runbook:  &RunbookRef{ID: "runbook:network-verification", Revision: "v1"},
		Evidence: []EvidenceRef{evidence},
		Timeline: []TimelineEntry{
			{Kind: TimelineOpened, At: started},
			{Kind: TimelineSignalObserved, At: observed, Summary: "Зафиксирован сигнал потери freshness", Evidence: []EvidenceRef{evidence}},
		},
	}
	if status == StatusAcknowledged || status == StatusResolved {
		incident.Acknowledgement = &Acknowledgement{ActorID: "user:admin", At: ackAt}
		incident.Timeline = append(incident.Timeline, TimelineEntry{Kind: TimelineAcknowledged, At: ackAt, ActorID: "user:admin", Summary: "Инцидент принят в работу"})
	}
	if status == StatusResolved {
		resolvedAt := ackAt.Add(time.Minute)
		incident.UpdatedAt = resolvedAt
		incident.Timeline = append(incident.Timeline, TimelineEntry{Kind: TimelineResolved, At: resolvedAt, ActorID: "user:admin", Summary: "Свежесть проверки восстановлена"})
	}
	return incident
}
