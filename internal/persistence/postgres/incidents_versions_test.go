package postgres

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"control-center/internal/corecontracts"
	"control-center/internal/incidents"
)

func TestIncidentResourceVersionGeneratorProducesOpaqueStableShape(t *testing.T) {
	entropy := bytes.Repeat([]byte{0xab}, incidentResourceVersionEntropyBytes)
	generator := newIncidentResourceVersionGenerator(bytes.NewReader(entropy))
	current := validIncidentForVersionTest()

	version, err := generator.NextIncidentResourceVersion(context.Background(), current)
	if err != nil {
		t.Fatalf("NextIncidentResourceVersion() error = %v", err)
	}
	want := "irv-" + strings.Repeat("ab", incidentResourceVersionEntropyBytes)
	if version != want {
		t.Fatalf("version = %q, want %q", version, want)
	}
	if version == current.ResourceVersion {
		t.Fatal("resource version must change")
	}
}

func TestIncidentResourceVersionGeneratorFailsClosedOnEntropyError(t *testing.T) {
	generator := newIncidentResourceVersionGenerator(bytes.NewReader(nil))
	if _, err := generator.NextIncidentResourceVersion(context.Background(), validIncidentForVersionTest()); err == nil {
		t.Fatal("expected entropy failure")
	}
}

func validIncidentForVersionTest() incidents.Incident {
	started := time.Date(2026, 9, 12, 19, 0, 0, 0, time.UTC)
	observed := started.Add(time.Minute)
	evidence := incidents.EvidenceRef{
		Kind:      "audit-event",
		ID:        "audit:version-test",
		SHA256:    strings.Repeat("a", 64),
		Collected: observed,
		Redaction: incidents.RedactionApplied,
	}
	return incidents.Incident{
		ObjectMetadata: corecontracts.ObjectMetadata{
			ObjectID:        "incident:version-test",
			ScopeID:         "site:primary",
			OwnerScope:      "installation:default",
			Generation:      1,
			ResourceVersion: "rv-current",
			CreatedAt:       started,
			UpdatedAt:       observed,
		},
		Severity:       incidents.SeverityWarning,
		Status:         incidents.StatusOpen,
		Title:          "Проверка генератора resource version",
		StartedAt:      started,
		LastObservedAt: observed,
		AffectedResources: []incidents.ResourceRef{
			{Kind: "node", ID: "node-1", ScopeID: "site:primary"},
		},
		Signals: []incidents.Signal{
			{ID: "signal-1", Kind: "health-degraded", Source: "core-health", ObservedAt: observed, Summary: "Наблюдается деградация", Evidence: []incidents.EvidenceRef{evidence}},
		},
		Timeline: []incidents.TimelineEntry{
			{Kind: incidents.TimelineOpened, At: started},
			{Kind: incidents.TimelineSignalObserved, At: observed, Summary: "Зафиксирован сигнал", Evidence: []incidents.EvidenceRef{evidence}},
		},
		Evidence: []incidents.EvidenceRef{evidence},
	}
}
