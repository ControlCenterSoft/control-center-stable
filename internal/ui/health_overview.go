package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	HealthOverviewSchemaV1 = "ui.health-overview/v1"

	HealthDataLoaded      = "loaded"
	HealthDataUnavailable = "unavailable"

	HealthStateHealthy   = "healthy"
	HealthStateUnknown   = "unknown"
	HealthStateDegraded  = "degraded"
	HealthStateUnhealthy = "unhealthy"

	HealthFreshnessCurrent = "current"
	HealthFreshnessStale   = "stale"
	HealthFreshnessExpired = "expired"

	maxHealthSignals      = 1000
	maxEvidenceRefs       = 32
	maxSignalIDLength     = 128
	maxResourceKindLength = 64
	maxResourceIDLength   = 256
	maxCheckNameLength    = 128
	maxRunbookRefLength   = 256
	maxEvidenceRefLength  = 256
)

// HealthSignal is bounded, provider-neutral observation evidence for one
// resource check. It intentionally carries no raw provider error/payload,
// credential, worker identity or transport detail.
type HealthSignal struct {
	ID           string    `json:"id"`
	ResourceKind string    `json:"resource_kind"`
	ResourceID   string    `json:"resource_id"`
	CheckName    string    `json:"check_name"`
	State        string    `json:"state"`
	ObservedAt   time.Time `json:"observed_at"`
	RunbookRef   string    `json:"runbook_ref,omitempty"`
	EvidenceRefs []string  `json:"evidence_refs,omitempty"`
}

// HealthOverviewInput defines the trusted-clock freshness boundary used to
// build a read-only Health view. Loaded=false means the authoritative source
// could not be read; it must not be combined with partial signal data.
type HealthOverviewInput struct {
	Loaded       bool
	Signals      []HealthSignal
	Now          time.Time
	StaleAfter   time.Duration
	ExpiredAfter time.Duration
}

type HealthSignalView struct {
	ID             string    `json:"id"`
	ResourceKind   string    `json:"resource_kind"`
	ResourceID     string    `json:"resource_id"`
	CheckName      string    `json:"check_name"`
	ObservedState  string    `json:"observed_state"`
	EffectiveState string    `json:"effective_state"`
	Freshness      string    `json:"freshness"`
	ObservedAt     time.Time `json:"observed_at"`
	RunbookRef     string    `json:"runbook_ref,omitempty"`
	EvidenceRefs   []string  `json:"evidence_refs,omitempty"`
}

type ResourceHealthView struct {
	ResourceKind string   `json:"resource_kind"`
	ResourceID   string   `json:"resource_id"`
	State        string   `json:"state"`
	Freshness    string   `json:"freshness"`
	SignalIDs    []string `json:"signal_ids"`
}

type HealthOverview struct {
	Schema             string               `json:"schema"`
	DataState          string               `json:"data_state"`
	OverallState       string               `json:"overall_state"`
	Resources          []ResourceHealthView `json:"resources"`
	Signals            []HealthSignalView   `json:"signals"`
	MutationAuthorized bool                 `json:"mutation_authorized"`
}

// BuildHealthOverview creates a deterministic, fail-closed read model. Healthy
// observations are degraded when stale and become unknown when expired.
// Observed degraded/unhealthy states are never improved merely because their
// evidence aged. The result never grants mutation or execution authority.
func BuildHealthOverview(input HealthOverviewInput) (HealthOverview, error) {
	if input.Now.IsZero() {
		return HealthOverview{}, fmt.Errorf("trusted now is required")
	}
	if input.StaleAfter <= 0 || input.ExpiredAfter <= input.StaleAfter {
		return HealthOverview{}, fmt.Errorf("freshness budgets must satisfy 0 < stale_after < expired_after")
	}
	if len(input.Signals) > maxHealthSignals {
		return HealthOverview{}, fmt.Errorf("health signal limit exceeded")
	}
	if !input.Loaded {
		if len(input.Signals) != 0 {
			return HealthOverview{}, fmt.Errorf("unavailable source cannot include partial signals")
		}
		return HealthOverview{
			Schema: HealthOverviewSchemaV1, DataState: HealthDataUnavailable,
			OverallState: HealthStateUnknown, Resources: []ResourceHealthView{},
			Signals: []HealthSignalView{}, MutationAuthorized: false,
		}, nil
	}

	now := input.Now.UTC()
	views := make([]HealthSignalView, 0, len(input.Signals))
	seen := make(map[string]struct{}, len(input.Signals))
	for _, signal := range input.Signals {
		view, err := normalizeHealthSignal(signal, now, input.StaleAfter, input.ExpiredAfter)
		if err != nil {
			return HealthOverview{}, err
		}
		if _, exists := seen[view.ID]; exists {
			return HealthOverview{}, fmt.Errorf("duplicate health signal id %q", view.ID)
		}
		seen[view.ID] = struct{}{}
		views = append(views, view)
	}

	sort.Slice(views, func(i, j int) bool {
		if healthStateRank(views[i].EffectiveState) != healthStateRank(views[j].EffectiveState) {
			return healthStateRank(views[i].EffectiveState) > healthStateRank(views[j].EffectiveState)
		}
		if healthFreshnessRank(views[i].Freshness) != healthFreshnessRank(views[j].Freshness) {
			return healthFreshnessRank(views[i].Freshness) > healthFreshnessRank(views[j].Freshness)
		}
		if views[i].ResourceKind != views[j].ResourceKind {
			return views[i].ResourceKind < views[j].ResourceKind
		}
		if views[i].ResourceID != views[j].ResourceID {
			return views[i].ResourceID < views[j].ResourceID
		}
		if views[i].CheckName != views[j].CheckName {
			return views[i].CheckName < views[j].CheckName
		}
		return views[i].ID < views[j].ID
	})

	resources := aggregateResourceHealth(views)
	overall := HealthStateUnknown
	if len(resources) > 0 {
		overall = resources[0].State
		for _, resource := range resources[1:] {
			if healthStateRank(resource.State) > healthStateRank(overall) {
				overall = resource.State
			}
		}
	}

	return HealthOverview{
		Schema: HealthOverviewSchemaV1, DataState: HealthDataLoaded,
		OverallState: overall, Resources: resources, Signals: views,
		MutationAuthorized: false,
	}, nil
}

func normalizeHealthSignal(signal HealthSignal, now time.Time, staleAfter, expiredAfter time.Duration) (HealthSignalView, error) {
	signal.ID = strings.TrimSpace(signal.ID)
	signal.ResourceKind = strings.TrimSpace(signal.ResourceKind)
	signal.ResourceID = strings.TrimSpace(signal.ResourceID)
	signal.CheckName = strings.TrimSpace(signal.CheckName)
	signal.State = strings.ToLower(strings.TrimSpace(signal.State))
	signal.RunbookRef = strings.TrimSpace(signal.RunbookRef)

	if err := validateHealthBoundedText("signal id", signal.ID, maxSignalIDLength); err != nil {
		return HealthSignalView{}, err
	}
	if err := validateHealthBoundedText("resource kind", signal.ResourceKind, maxResourceKindLength); err != nil {
		return HealthSignalView{}, err
	}
	if err := validateHealthBoundedText("resource id", signal.ResourceID, maxResourceIDLength); err != nil {
		return HealthSignalView{}, err
	}
	if err := validateHealthBoundedText("check name", signal.CheckName, maxCheckNameLength); err != nil {
		return HealthSignalView{}, err
	}
	if signal.RunbookRef != "" {
		if err := validateHealthBoundedText("runbook ref", signal.RunbookRef, maxRunbookRefLength); err != nil {
			return HealthSignalView{}, err
		}
	}
	if healthStateRank(signal.State) < 0 {
		return HealthSignalView{}, fmt.Errorf("unsupported health state %q", signal.State)
	}
	if signal.ObservedAt.IsZero() {
		return HealthSignalView{}, fmt.Errorf("observed_at is required for signal %q", signal.ID)
	}
	observedAt := signal.ObservedAt.UTC()
	if observedAt.After(now) {
		return HealthSignalView{}, fmt.Errorf("future health evidence for signal %q", signal.ID)
	}

	refs, err := normalizeHealthEvidenceRefs(signal.EvidenceRefs)
	if err != nil {
		return HealthSignalView{}, fmt.Errorf("signal %q: %w", signal.ID, err)
	}
	freshness := effectiveHealthFreshness(now.Sub(observedAt), staleAfter, expiredAfter)
	state := effectiveHealthState(signal.State, freshness)

	return HealthSignalView{
		ID: signal.ID, ResourceKind: signal.ResourceKind, ResourceID: signal.ResourceID,
		CheckName: signal.CheckName, ObservedState: signal.State, EffectiveState: state,
		Freshness: freshness, ObservedAt: observedAt, RunbookRef: signal.RunbookRef,
		EvidenceRefs: refs,
	}, nil
}

func validateHealthBoundedText(field, value string, maximum int) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maximum {
		return fmt.Errorf("%s exceeds maximum length or is invalid UTF-8", field)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains control characters", field)
		}
	}
	return nil
}

func normalizeHealthEvidenceRefs(values []string) ([]string, error) {
	if len(values) > maxEvidenceRefs {
		return nil, fmt.Errorf("evidence reference limit exceeded")
	}
	seen := make(map[string]struct{}, len(values))
	refs := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if err := validateHealthBoundedText("evidence reference", value, maxEvidenceRefLength); err != nil {
			return nil, err
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		refs = append(refs, value)
	}
	sort.Strings(refs)
	return refs, nil
}

func effectiveHealthFreshness(age, staleAfter, expiredAfter time.Duration) string {
	if age > expiredAfter {
		return HealthFreshnessExpired
	}
	if age > staleAfter {
		return HealthFreshnessStale
	}
	return HealthFreshnessCurrent
}

func effectiveHealthState(observed, freshness string) string {
	if observed != HealthStateHealthy {
		return observed
	}
	switch freshness {
	case HealthFreshnessExpired:
		return HealthStateUnknown
	case HealthFreshnessStale:
		return HealthStateDegraded
	default:
		return HealthStateHealthy
	}
}

func aggregateResourceHealth(signals []HealthSignalView) []ResourceHealthView {
	type key struct{ kind, id string }
	resources := make(map[key]*ResourceHealthView)
	for _, signal := range signals {
		k := key{kind: signal.ResourceKind, id: signal.ResourceID}
		resource, exists := resources[k]
		if !exists {
			resource = &ResourceHealthView{
				ResourceKind: signal.ResourceKind, ResourceID: signal.ResourceID,
				State: signal.EffectiveState, Freshness: signal.Freshness,
			}
			resources[k] = resource
		}
		if healthStateRank(signal.EffectiveState) > healthStateRank(resource.State) {
			resource.State = signal.EffectiveState
		}
		if healthFreshnessRank(signal.Freshness) > healthFreshnessRank(resource.Freshness) {
			resource.Freshness = signal.Freshness
		}
		resource.SignalIDs = append(resource.SignalIDs, signal.ID)
	}

	views := make([]ResourceHealthView, 0, len(resources))
	for _, resource := range resources {
		sort.Strings(resource.SignalIDs)
		views = append(views, *resource)
	}
	sort.Slice(views, func(i, j int) bool {
		if healthStateRank(views[i].State) != healthStateRank(views[j].State) {
			return healthStateRank(views[i].State) > healthStateRank(views[j].State)
		}
		if healthFreshnessRank(views[i].Freshness) != healthFreshnessRank(views[j].Freshness) {
			return healthFreshnessRank(views[i].Freshness) > healthFreshnessRank(views[j].Freshness)
		}
		if views[i].ResourceKind != views[j].ResourceKind {
			return views[i].ResourceKind < views[j].ResourceKind
		}
		return views[i].ResourceID < views[j].ResourceID
	})
	return views
}

func healthStateRank(state string) int {
	switch state {
	case HealthStateHealthy:
		return 0
	case HealthStateUnknown:
		return 1
	case HealthStateDegraded:
		return 2
	case HealthStateUnhealthy:
		return 3
	default:
		return -1
	}
}

func healthFreshnessRank(freshness string) int {
	switch freshness {
	case HealthFreshnessCurrent:
		return 0
	case HealthFreshnessStale:
		return 1
	case HealthFreshnessExpired:
		return 2
	default:
		return -1
	}
}
