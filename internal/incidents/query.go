package incidents

import (
	"fmt"
	"sort"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	DefaultListLimit = 50
	MaxListLimit     = 100
	maxCursorIDBytes = 255
)

// ListCursor is a deterministic keyset cursor for the canonical newest-first
// incident order. StartedAt sorts descending and ObjectID breaks ties ascending.
type ListCursor struct {
	StartedAt time.Time `json:"started_at"`
	ObjectID  string    `json:"object_id"`
}

// ListQuery intentionally exposes only bounded exact filters. Full-text search
// and provider-specific expressions do not belong in the Core incident API.
type ListQuery struct {
	Limit         int         `json:"limit,omitempty"`
	Statuses      []Status    `json:"statuses,omitempty"`
	Severities    []Severity  `json:"severities,omitempty"`
	ScopeID       string      `json:"scope_id,omitempty"`
	ResourceKind  string      `json:"resource_kind,omitempty"`
	ResourceID    string      `json:"resource_id,omitempty"`
	StartedFrom   *time.Time  `json:"started_from,omitempty"`
	StartedBefore *time.Time  `json:"started_before,omitempty"`
	Before        *ListCursor `json:"before,omitempty"`
}

// ListPage is a bounded page. Next is present only when another page is known
// to exist; callers must pass it back unchanged instead of inventing cursors.
type ListPage struct {
	Items   []Incident  `json:"items"`
	Next    *ListCursor `json:"next,omitempty"`
	HasMore bool        `json:"has_more"`
}

// NormalizeListQuery validates and canonicalizes a list query without reading
// storage. Filter sets are sorted to make cache/audit keys deterministic.
func NormalizeListQuery(query ListQuery) (ListQuery, error) {
	if query.Limit == 0 {
		query.Limit = DefaultListLimit
	}
	if query.Limit < 1 || query.Limit > MaxListLimit {
		return ListQuery{}, fmt.Errorf("incident list limit must be between 1 and %d", MaxListLimit)
	}

	statuses, err := normalizeStatuses(query.Statuses)
	if err != nil {
		return ListQuery{}, err
	}
	query.Statuses = statuses
	severities, err := normalizeSeverities(query.Severities)
	if err != nil {
		return ListQuery{}, err
	}
	query.Severities = severities

	if query.ScopeID != "" {
		if err := validateRef("scope_id", query.ScopeID); err != nil {
			return ListQuery{}, fmt.Errorf("invalid incident scope filter: %w", err)
		}
	}
	if query.ResourceKind != "" {
		if err := validateRef("resource_kind", query.ResourceKind); err != nil {
			return ListQuery{}, fmt.Errorf("invalid incident resource filter: %w", err)
		}
	}
	if query.ResourceID != "" {
		if query.ResourceKind == "" {
			return ListQuery{}, fmt.Errorf("incident resource_id filter requires resource_kind")
		}
		if err := validateRef("resource_id", query.ResourceID); err != nil {
			return ListQuery{}, fmt.Errorf("invalid incident resource filter: %w", err)
		}
	}

	if query.StartedFrom != nil {
		if query.StartedFrom.IsZero() {
			return ListQuery{}, fmt.Errorf("incident started_from must not be zero")
		}
		value := query.StartedFrom.UTC()
		query.StartedFrom = &value
	}
	if query.StartedBefore != nil {
		if query.StartedBefore.IsZero() {
			return ListQuery{}, fmt.Errorf("incident started_before must not be zero")
		}
		value := query.StartedBefore.UTC()
		query.StartedBefore = &value
	}
	if query.StartedFrom != nil && query.StartedBefore != nil && !query.StartedFrom.Before(*query.StartedBefore) {
		return ListQuery{}, fmt.Errorf("incident started_from must precede started_before")
	}

	if query.Before != nil {
		if query.Before.StartedAt.IsZero() {
			return ListQuery{}, fmt.Errorf("incident cursor started_at is required")
		}
		if err := validateCursorID(query.Before.ObjectID); err != nil {
			return ListQuery{}, err
		}
		query.Before = &ListCursor{StartedAt: query.Before.StartedAt.UTC(), ObjectID: query.Before.ObjectID}
	}
	return query, nil
}

func normalizeStatuses(values []Status) ([]Status, error) {
	if len(values) > 3 {
		return nil, fmt.Errorf("incident status filter exceeds supported values")
	}
	seen := make(map[Status]struct{}, len(values))
	out := append([]Status(nil), values...)
	for _, value := range out {
		if !value.Valid() {
			return nil, fmt.Errorf("unsupported incident status filter %q", value)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("duplicate incident status filter %q", value)
		}
		seen[value] = struct{}{}
	}
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out, nil
}

func normalizeSeverities(values []Severity) ([]Severity, error) {
	if len(values) > 3 {
		return nil, fmt.Errorf("incident severity filter exceeds supported values")
	}
	seen := make(map[Severity]struct{}, len(values))
	out := append([]Severity(nil), values...)
	for _, value := range out {
		if !value.Valid() {
			return nil, fmt.Errorf("unsupported incident severity filter %q", value)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("duplicate incident severity filter %q", value)
		}
		seen[value] = struct{}{}
	}
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out, nil
}

func validateCursorID(value string) error {
	if value == "" || len(value) > maxCursorIDBytes || !utf8.ValidString(value) {
		return fmt.Errorf("incident cursor object_id is invalid")
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("incident cursor object_id contains whitespace or control characters")
		}
	}
	if err := validateRef("cursor.object_id", value); err != nil {
		return fmt.Errorf("incident cursor object_id is invalid: %w", err)
	}
	return nil
}

// FilterPage provides reference semantics for storage adapters and deterministic
// fixtures. Malformed stored incidents fail the entire page rather than being
// silently hidden from operators.
func FilterPage(input []Incident, query ListQuery) (ListPage, error) {
	query, err := NormalizeListQuery(query)
	if err != nil {
		return ListPage{}, err
	}
	ordered := append([]Incident(nil), input...)
	for idx := range ordered {
		if err := ordered[idx].Validate(); err != nil {
			return ListPage{}, fmt.Errorf("incident list contains invalid item %d: %w", idx, err)
		}
	}
	sort.SliceStable(ordered, func(a, b int) bool {
		if ordered[a].StartedAt.Equal(ordered[b].StartedAt) {
			return ordered[a].ObjectID < ordered[b].ObjectID
		}
		return ordered[a].StartedAt.After(ordered[b].StartedAt)
	})

	items := make([]Incident, 0, min(query.Limit+1, len(ordered)))
	for _, incident := range ordered {
		if !matchesListQuery(incident, query) {
			continue
		}
		items = append(items, incident)
		if len(items) > query.Limit {
			break
		}
	}

	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	page := ListPage{Items: items, HasMore: hasMore}
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		page.Next = &ListCursor{StartedAt: last.StartedAt.UTC(), ObjectID: last.ObjectID}
	}
	return page, nil
}

func matchesListQuery(incident Incident, query ListQuery) bool {
	if len(query.Statuses) > 0 && !containsStatus(query.Statuses, incident.Status) {
		return false
	}
	if len(query.Severities) > 0 && !containsSeverity(query.Severities, incident.Severity) {
		return false
	}
	if query.ScopeID != "" && incident.ScopeID != query.ScopeID {
		return false
	}
	if query.StartedFrom != nil && incident.StartedAt.Before(*query.StartedFrom) {
		return false
	}
	if query.StartedBefore != nil && !incident.StartedAt.Before(*query.StartedBefore) {
		return false
	}
	if query.Before != nil {
		if incident.StartedAt.After(query.Before.StartedAt) {
			return false
		}
		if incident.StartedAt.Equal(query.Before.StartedAt) && incident.ObjectID <= query.Before.ObjectID {
			return false
		}
	}
	if query.ResourceKind != "" {
		matched := false
		for _, resource := range incident.AffectedResources {
			if resource.Kind != query.ResourceKind {
				continue
			}
			if query.ResourceID != "" && resource.ID != query.ResourceID {
				continue
			}
			matched = true
			break
		}
		if !matched {
			return false
		}
	}
	return true
}

func containsStatus(values []Status, want Status) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsSeverity(values []Severity, want Severity) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
