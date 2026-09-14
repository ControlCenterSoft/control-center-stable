package ui

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBuildHealthOverviewFreshnessFailsClosed(t *testing.T) {
	now := time.Date(2026, 9, 12, 15, 0, 0, 0, time.UTC)
	input := HealthOverviewInput{
		Loaded: true, Now: now, StaleAfter: 5 * time.Minute, ExpiredAfter: 15 * time.Minute,
		Signals: []HealthSignal{
			{ID: "current", ResourceKind: "node", ResourceID: "n1", CheckName: "api", State: HealthStateHealthy, ObservedAt: now.Add(-2 * time.Minute)},
			{ID: "stale", ResourceKind: "node", ResourceID: "n2", CheckName: "api", State: HealthStateHealthy, ObservedAt: now.Add(-10 * time.Minute)},
			{ID: "expired", ResourceKind: "node", ResourceID: "n3", CheckName: "api", State: HealthStateHealthy, ObservedAt: now.Add(-20 * time.Minute)},
			{ID: "unhealthy", ResourceKind: "node", ResourceID: "n4", CheckName: "api", State: HealthStateUnhealthy, ObservedAt: now.Add(-20 * time.Minute)},
		},
	}

	got, err := BuildHealthOverview(input)
	if err != nil {
		t.Fatalf("BuildHealthOverview() error = %v", err)
	}
	if got.MutationAuthorized {
		t.Fatal("health overview must never grant mutation authority")
	}
	if got.OverallState != HealthStateUnhealthy {
		t.Fatalf("overall state = %q, want unhealthy", got.OverallState)
	}

	states := map[string]HealthSignalView{}
	for _, signal := range got.Signals {
		states[signal.ID] = signal
	}
	if states["current"].EffectiveState != HealthStateHealthy || states["current"].Freshness != HealthFreshnessCurrent {
		t.Fatalf("current healthy signal = %#v", states["current"])
	}
	if states["stale"].EffectiveState != HealthStateDegraded || states["stale"].Freshness != HealthFreshnessStale {
		t.Fatalf("stale healthy signal = %#v", states["stale"])
	}
	if states["expired"].EffectiveState != HealthStateUnknown || states["expired"].Freshness != HealthFreshnessExpired {
		t.Fatalf("expired healthy signal = %#v", states["expired"])
	}
	if states["unhealthy"].EffectiveState != HealthStateUnhealthy || states["unhealthy"].Freshness != HealthFreshnessExpired {
		t.Fatalf("expired unhealthy signal must not be improved = %#v", states["unhealthy"])
	}
}

func TestBuildHealthOverviewUnavailableAndEmptyNeverHealthy(t *testing.T) {
	now := time.Date(2026, 9, 12, 15, 0, 0, 0, time.UTC)
	for name, input := range map[string]HealthOverviewInput{
		"unavailable": {Loaded: false, Now: now, StaleAfter: time.Minute, ExpiredAfter: 2 * time.Minute},
		"empty":       {Loaded: true, Now: now, StaleAfter: time.Minute, ExpiredAfter: 2 * time.Minute},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := BuildHealthOverview(input)
			if err != nil {
				t.Fatalf("BuildHealthOverview() error = %v", err)
			}
			if got.OverallState != HealthStateUnknown {
				t.Fatalf("overall state = %q, want unknown", got.OverallState)
			}
			if len(got.Resources) != 0 || len(got.Signals) != 0 {
				t.Fatalf("unexpected partial evidence: resources=%d signals=%d", len(got.Resources), len(got.Signals))
			}
		})
	}
}

func TestBuildHealthOverviewRejectsUnavailablePartialDuplicateAndFutureEvidence(t *testing.T) {
	now := time.Date(2026, 9, 12, 15, 0, 0, 0, time.UTC)
	base := HealthSignal{ID: "sig-1", ResourceKind: "node", ResourceID: "n1", CheckName: "api", State: HealthStateHealthy, ObservedAt: now}

	tests := map[string]HealthOverviewInput{
		"unavailable-partial": {Loaded: false, Now: now, StaleAfter: time.Minute, ExpiredAfter: 2 * time.Minute, Signals: []HealthSignal{base}},
		"duplicate":           {Loaded: true, Now: now, StaleAfter: time.Minute, ExpiredAfter: 2 * time.Minute, Signals: []HealthSignal{base, base}},
		"future":              {Loaded: true, Now: now, StaleAfter: time.Minute, ExpiredAfter: 2 * time.Minute, Signals: []HealthSignal{{ID: "future", ResourceKind: "node", ResourceID: "n1", CheckName: "api", State: HealthStateHealthy, ObservedAt: now.Add(time.Second)}}},
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := BuildHealthOverview(input); err == nil {
				t.Fatal("expected fail-closed error")
			}
		})
	}
}

func TestBuildHealthOverviewNormalizesEvidenceAndAggregatesWorstResourceState(t *testing.T) {
	now := time.Date(2026, 9, 12, 15, 0, 0, 0, time.UTC)
	got, err := BuildHealthOverview(HealthOverviewInput{
		Loaded: true, Now: now, StaleAfter: 5 * time.Minute, ExpiredAfter: 15 * time.Minute,
		Signals: []HealthSignal{
			{ID: "b", ResourceKind: "node", ResourceID: "n1", CheckName: "storage", State: HealthStateHealthy, ObservedAt: now, EvidenceRefs: []string{" evidence:b ", "evidence:a", "evidence:b"}},
			{ID: "a", ResourceKind: "node", ResourceID: "n1", CheckName: "api", State: HealthStateDegraded, ObservedAt: now.Add(-10 * time.Minute)},
		},
	})
	if err != nil {
		t.Fatalf("BuildHealthOverview() error = %v", err)
	}
	if len(got.Resources) != 1 {
		t.Fatalf("resource count = %d, want 1", len(got.Resources))
	}
	resource := got.Resources[0]
	if resource.State != HealthStateDegraded || resource.Freshness != HealthFreshnessStale {
		t.Fatalf("resource aggregate = %#v", resource)
	}
	if !reflect.DeepEqual(resource.SignalIDs, []string{"a", "b"}) {
		t.Fatalf("signal ids = %#v", resource.SignalIDs)
	}
	var healthy HealthSignalView
	for _, signal := range got.Signals {
		if signal.ID == "b" {
			healthy = signal
		}
	}
	if !reflect.DeepEqual(healthy.EvidenceRefs, []string{"evidence:a", "evidence:b"}) {
		t.Fatalf("normalized evidence refs = %#v", healthy.EvidenceRefs)
	}
}

func TestBuildHealthOverviewDeterministicOrdering(t *testing.T) {
	now := time.Date(2026, 9, 12, 15, 0, 0, 0, time.UTC)
	input := HealthOverviewInput{
		Loaded: true, Now: now, StaleAfter: time.Minute, ExpiredAfter: 2 * time.Minute,
		Signals: []HealthSignal{
			{ID: "healthy", ResourceKind: "node", ResourceID: "b", CheckName: "api", State: HealthStateHealthy, ObservedAt: now},
			{ID: "unknown", ResourceKind: "node", ResourceID: "a", CheckName: "api", State: HealthStateUnknown, ObservedAt: now},
			{ID: "bad", ResourceKind: "site", ResourceID: "a", CheckName: "api", State: HealthStateUnhealthy, ObservedAt: now},
		},
	}
	got, err := BuildHealthOverview(input)
	if err != nil {
		t.Fatalf("BuildHealthOverview() error = %v", err)
	}
	ids := []string{got.Signals[0].ID, got.Signals[1].ID, got.Signals[2].ID}
	if !reflect.DeepEqual(ids, []string{"bad", "unknown", "healthy"}) {
		t.Fatalf("ordered signal ids = %#v", ids)
	}
}

func TestBuildHealthOverviewRejectsInvalidBounds(t *testing.T) {
	now := time.Date(2026, 9, 12, 15, 0, 0, 0, time.UTC)
	base := HealthOverviewInput{Loaded: true, Now: now, StaleAfter: time.Minute, ExpiredAfter: 2 * time.Minute}

	badBudget := base
	badBudget.ExpiredAfter = badBudget.StaleAfter
	if _, err := BuildHealthOverview(badBudget); err == nil {
		t.Fatal("expected invalid freshness budget to fail")
	}

	tooManyRefs := base
	refs := make([]string, maxEvidenceRefs+1)
	for i := range refs {
		refs[i] = "evidence:x"
	}
	tooManyRefs.Signals = []HealthSignal{{ID: "sig", ResourceKind: "node", ResourceID: "n1", CheckName: "api", State: HealthStateHealthy, ObservedAt: now, EvidenceRefs: refs}}
	if _, err := BuildHealthOverview(tooManyRefs); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("expected evidence-ref limit error, got %v", err)
	}
}

func TestBuildHealthOverviewRejectsControlCharactersInBoundedFields(t *testing.T) {
	now := time.Date(2026, 9, 12, 15, 0, 0, 0, time.UTC)
	base := HealthSignal{ID: "sig", ResourceKind: "node", ResourceID: "n1", CheckName: "api", State: HealthStateHealthy, ObservedAt: now}
	cases := []HealthSignal{
		func() HealthSignal { v := base; v.ID = "sig\nsecret"; return v }(),
		func() HealthSignal { v := base; v.ResourceKind = "node\tsecret"; return v }(),
		func() HealthSignal { v := base; v.ResourceID = "node\rsecret"; return v }(),
		func() HealthSignal { v := base; v.CheckName = "api\nsecret"; return v }(),
		func() HealthSignal { v := base; v.RunbookRef = "runbook:\tsecret"; return v }(),
		func() HealthSignal { v := base; v.EvidenceRefs = []string{"evidence:\nsecret"}; return v }(),
	}
	for i, signal := range cases {
		_, err := BuildHealthOverview(HealthOverviewInput{
			Loaded: true, Now: now, StaleAfter: time.Minute, ExpiredAfter: 2 * time.Minute,
			Signals: []HealthSignal{signal},
		})
		if err == nil {
			t.Fatalf("case %d accepted control characters", i)
		}
	}
}
