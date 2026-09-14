// Package incidents defines the side-effect-free incident read-model contract
// used by the Control Center 0.32 Health / Incidents / Audit / Reports UI.
// Persistence, RBAC, redaction policy, signal ingestion and audit emission are
// adapters around this contract and must remain fail-closed.
package incidents

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"control-center/internal/corecontracts"
)

const (
	maxTitleLength      = 160
	maxSummaryLength    = 512
	maxReferenceLength  = 255
	maxAffectedResource = 64
	maxSignals          = 128
	maxTimelineEntries  = 256
	maxEvidenceRefs     = 128
)

var (
	ErrInvalidIncident        = errors.New("invalid incident")
	ErrInvalidTimeline        = errors.New("invalid incident timeline")
	ErrInvalidEvidence        = errors.New("invalid incident evidence")
	ErrInvalidAcknowledgement = errors.New("invalid incident acknowledgement")
)

var refPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._:/-]{0,253}[A-Za-z0-9])?$`)
var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

func (s Severity) Valid() bool {
	return s == SeverityInfo || s == SeverityWarning || s == SeverityCritical
}

type Status string

const (
	StatusOpen         Status = "open"
	StatusAcknowledged Status = "acknowledged"
	StatusResolved     Status = "resolved"
)

func (s Status) Valid() bool {
	return s == StatusOpen || s == StatusAcknowledged || s == StatusResolved
}

type TimelineKind string

const (
	TimelineOpened         TimelineKind = "opened"
	TimelineSignalObserved TimelineKind = "signal-observed"
	TimelineAcknowledged   TimelineKind = "acknowledged"
	TimelineResolved       TimelineKind = "resolved"
	TimelineNote           TimelineKind = "note"
)

func (k TimelineKind) Valid() bool {
	switch k {
	case TimelineOpened, TimelineSignalObserved, TimelineAcknowledged, TimelineResolved, TimelineNote:
		return true
	default:
		return false
	}
}

type RedactionState string

const (
	RedactionNotApplicable RedactionState = "not-applicable"
	RedactionApplied       RedactionState = "applied"
)

func (s RedactionState) Valid() bool {
	return s == RedactionNotApplicable || s == RedactionApplied
}

// ResourceRef is a bounded reference to an affected managed object. It never
// contains an arbitrary serialized object payload.
type ResourceRef struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	ScopeID string `json:"scope_id"`
}

// EvidenceRef points to immutable or otherwise authoritative evidence without
// embedding its potentially sensitive payload in the incident read model.
type EvidenceRef struct {
	Kind      string         `json:"kind"`
	ID        string         `json:"id"`
	SHA256    string         `json:"sha256,omitempty"`
	Collected time.Time      `json:"collected_at"`
	Redaction RedactionState `json:"redaction"`
}

// Signal captures one bounded observation used to explain why the incident is
// open. Source identifies the collector/provider; Summary is human-readable
// and intentionally bounded.
type Signal struct {
	ID         string        `json:"id"`
	Kind       string        `json:"kind"`
	Source     string        `json:"source"`
	ObservedAt time.Time     `json:"observed_at"`
	Summary    string        `json:"summary"`
	Evidence   []EvidenceRef `json:"evidence,omitempty"`
}

type RunbookRef struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

type Acknowledgement struct {
	ActorID string    `json:"actor_id"`
	At      time.Time `json:"at"`
}

type TimelineEntry struct {
	Kind     TimelineKind  `json:"kind"`
	At       time.Time     `json:"at"`
	ActorID  string        `json:"actor_id,omitempty"`
	Summary  string        `json:"summary,omitempty"`
	Evidence []EvidenceRef `json:"evidence,omitempty"`
}

// Incident is a versioned read model. Generation represents semantic incident
// state changes; signal/timeline persistence still receives a new resource
// version for every stored update.
type Incident struct {
	corecontracts.ObjectMetadata
	Severity          Severity         `json:"severity"`
	Status            Status           `json:"status"`
	Title             string           `json:"title"`
	StartedAt         time.Time        `json:"started_at"`
	LastObservedAt    time.Time        `json:"last_observed_at"`
	AffectedResources []ResourceRef    `json:"affected_resources"`
	Signals           []Signal         `json:"signals"`
	Timeline          []TimelineEntry  `json:"timeline"`
	Runbook           *RunbookRef      `json:"runbook,omitempty"`
	Acknowledgement   *Acknowledgement `json:"acknowledgement,omitempty"`
	Evidence          []EvidenceRef    `json:"evidence,omitempty"`
}

func (i Incident) Validate() error {
	if err := i.ObjectMetadata.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidIncident, err)
	}
	if !i.Severity.Valid() {
		return fmt.Errorf("%w: unsupported severity %q", ErrInvalidIncident, i.Severity)
	}
	if !i.Status.Valid() {
		return fmt.Errorf("%w: unsupported status %q", ErrInvalidIncident, i.Status)
	}
	if err := validateText("title", i.Title, maxTitleLength, true); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidIncident, err)
	}
	if i.StartedAt.IsZero() || i.LastObservedAt.IsZero() {
		return fmt.Errorf("%w: started_at and last_observed_at are required", ErrInvalidIncident)
	}
	if i.LastObservedAt.Before(i.StartedAt) {
		return fmt.Errorf("%w: last_observed_at precedes started_at", ErrInvalidIncident)
	}
	if i.StartedAt.Before(i.CreatedAt) || i.LastObservedAt.After(i.UpdatedAt) {
		return fmt.Errorf("%w: observation timestamps exceed object lifetime", ErrInvalidIncident)
	}
	if len(i.AffectedResources) == 0 || len(i.AffectedResources) > maxAffectedResource {
		return fmt.Errorf("%w: affected_resources must contain 1..%d entries", ErrInvalidIncident, maxAffectedResource)
	}
	if len(i.Signals) == 0 || len(i.Signals) > maxSignals {
		return fmt.Errorf("%w: signals must contain 1..%d entries", ErrInvalidIncident, maxSignals)
	}
	if len(i.Timeline) == 0 || len(i.Timeline) > maxTimelineEntries {
		return fmt.Errorf("%w: timeline must contain 1..%d entries", ErrInvalidTimeline, maxTimelineEntries)
	}
	if len(i.Evidence) > maxEvidenceRefs {
		return fmt.Errorf("%w: too many incident evidence references", ErrInvalidEvidence)
	}
	if err := validateResources(i.AffectedResources); err != nil {
		return err
	}
	if err := validateEvidenceSet(i.Evidence); err != nil {
		return err
	}
	if err := validateSignals(i.Signals, i.StartedAt, i.LastObservedAt); err != nil {
		return err
	}
	if i.Runbook != nil {
		if err := validateRef("runbook.id", i.Runbook.ID); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidIncident, err)
		}
		if err := validateRef("runbook.revision", i.Runbook.Revision); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidIncident, err)
		}
	}
	if err := validateAcknowledgement(i.Status, i.Acknowledgement, i.StartedAt, i.UpdatedAt); err != nil {
		return err
	}
	if err := validateTimeline(i); err != nil {
		return err
	}
	return nil
}

func validateResources(resources []ResourceRef) error {
	seen := make(map[string]struct{}, len(resources))
	for idx, resource := range resources {
		if err := validateRef("resource.kind", resource.Kind); err != nil {
			return fmt.Errorf("%w: affected_resources[%d]: %v", ErrInvalidIncident, idx, err)
		}
		if err := validateRef("resource.id", resource.ID); err != nil {
			return fmt.Errorf("%w: affected_resources[%d]: %v", ErrInvalidIncident, idx, err)
		}
		if err := validateRef("resource.scope_id", resource.ScopeID); err != nil {
			return fmt.Errorf("%w: affected_resources[%d]: %v", ErrInvalidIncident, idx, err)
		}
		key := resource.Kind + "\x00" + resource.ID + "\x00" + resource.ScopeID
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate affected resource %s/%s", ErrInvalidIncident, resource.Kind, resource.ID)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateSignals(signals []Signal, startedAt, lastObservedAt time.Time) error {
	seen := make(map[string]struct{}, len(signals))
	latest := startedAt
	for idx, signal := range signals {
		if err := validateRef("signal.id", signal.ID); err != nil {
			return fmt.Errorf("%w: signals[%d]: %v", ErrInvalidIncident, idx, err)
		}
		if _, exists := seen[signal.ID]; exists {
			return fmt.Errorf("%w: duplicate signal id %q", ErrInvalidIncident, signal.ID)
		}
		seen[signal.ID] = struct{}{}
		if err := validateRef("signal.kind", signal.Kind); err != nil {
			return fmt.Errorf("%w: signals[%d]: %v", ErrInvalidIncident, idx, err)
		}
		if err := validateRef("signal.source", signal.Source); err != nil {
			return fmt.Errorf("%w: signals[%d]: %v", ErrInvalidIncident, idx, err)
		}
		if signal.ObservedAt.IsZero() || signal.ObservedAt.Before(startedAt) || signal.ObservedAt.After(lastObservedAt) {
			return fmt.Errorf("%w: signals[%d] observed_at is outside incident observation window", ErrInvalidIncident, idx)
		}
		if signal.ObservedAt.After(latest) {
			latest = signal.ObservedAt
		}
		if err := validateText("signal.summary", signal.Summary, maxSummaryLength, true); err != nil {
			return fmt.Errorf("%w: signals[%d]: %v", ErrInvalidIncident, idx, err)
		}
		if len(signal.Evidence) > maxEvidenceRefs {
			return fmt.Errorf("%w: signals[%d] has too many evidence references", ErrInvalidEvidence, idx)
		}
		if err := validateEvidenceSet(signal.Evidence); err != nil {
			return fmt.Errorf("%w: signals[%d]: %v", ErrInvalidEvidence, idx, err)
		}
	}
	if !latest.Equal(lastObservedAt) {
		return fmt.Errorf("%w: last_observed_at does not match newest signal", ErrInvalidIncident)
	}
	return nil
}

func validateAcknowledgement(status Status, acknowledgement *Acknowledgement, startedAt, updatedAt time.Time) error {
	if status == StatusOpen {
		if acknowledgement != nil {
			return fmt.Errorf("%w: open incident must not carry acknowledgement", ErrInvalidAcknowledgement)
		}
		return nil
	}
	if acknowledgement == nil {
		return fmt.Errorf("%w: %s incident requires acknowledgement", ErrInvalidAcknowledgement, status)
	}
	if err := validateRef("acknowledgement.actor_id", acknowledgement.ActorID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAcknowledgement, err)
	}
	if acknowledgement.At.IsZero() || acknowledgement.At.Before(startedAt) || acknowledgement.At.After(updatedAt) {
		return fmt.Errorf("%w: acknowledgement timestamp is outside incident lifetime", ErrInvalidAcknowledgement)
	}
	return nil
}

func validateTimeline(incident Incident) error {
	entries := incident.Timeline
	if entries[0].Kind != TimelineOpened || !entries[0].At.Equal(incident.StartedAt) {
		return fmt.Errorf("%w: first entry must open the incident at started_at", ErrInvalidTimeline)
	}
	ackCount := 0
	resolvedCount := 0
	previous := time.Time{}
	for idx, entry := range entries {
		if !entry.Kind.Valid() {
			return fmt.Errorf("%w: timeline[%d] has unsupported kind %q", ErrInvalidTimeline, idx, entry.Kind)
		}
		if entry.At.IsZero() || entry.At.Before(incident.StartedAt) || entry.At.After(incident.UpdatedAt) {
			return fmt.Errorf("%w: timeline[%d] timestamp is outside incident lifetime", ErrInvalidTimeline, idx)
		}
		if !previous.IsZero() && entry.At.Before(previous) {
			return fmt.Errorf("%w: entries are not chronological", ErrInvalidTimeline)
		}
		previous = entry.At
		if err := validateText("timeline.summary", entry.Summary, maxSummaryLength, false); err != nil {
			return fmt.Errorf("%w: timeline[%d]: %v", ErrInvalidTimeline, idx, err)
		}
		if len(entry.Evidence) > maxEvidenceRefs {
			return fmt.Errorf("%w: timeline[%d] has too many evidence references", ErrInvalidEvidence, idx)
		}
		if err := validateEvidenceSet(entry.Evidence); err != nil {
			return fmt.Errorf("%w: timeline[%d]: %v", ErrInvalidEvidence, idx, err)
		}
		switch entry.Kind {
		case TimelineAcknowledged:
			ackCount++
			if incident.Acknowledgement == nil || entry.ActorID != incident.Acknowledgement.ActorID || !entry.At.Equal(incident.Acknowledgement.At) {
				return fmt.Errorf("%w: acknowledgement entry does not match acknowledgement state", ErrInvalidTimeline)
			}
		case TimelineResolved:
			resolvedCount++
			if entry.ActorID == "" {
				return fmt.Errorf("%w: resolved entry requires actor_id", ErrInvalidTimeline)
			}
		case TimelineOpened, TimelineSignalObserved:
			if entry.ActorID != "" {
				return fmt.Errorf("%w: system observation entry must not carry actor_id", ErrInvalidTimeline)
			}
		case TimelineNote:
			if entry.ActorID == "" {
				return fmt.Errorf("%w: note entry requires actor_id", ErrInvalidTimeline)
			}
		}
		if entry.ActorID != "" {
			if err := validateRef("timeline.actor_id", entry.ActorID); err != nil {
				return fmt.Errorf("%w: timeline[%d]: %v", ErrInvalidTimeline, idx, err)
			}
		}
	}
	if incident.Status == StatusOpen && (ackCount != 0 || resolvedCount != 0) {
		return fmt.Errorf("%w: open incident contains terminal operator events", ErrInvalidTimeline)
	}
	if incident.Status == StatusAcknowledged && (ackCount != 1 || resolvedCount != 0) {
		return fmt.Errorf("%w: acknowledged incident requires exactly one acknowledgement and no resolution", ErrInvalidTimeline)
	}
	if incident.Status == StatusResolved && (ackCount != 1 || resolvedCount != 1 || entries[len(entries)-1].Kind != TimelineResolved) {
		return fmt.Errorf("%w: resolved incident requires one acknowledgement and terminal resolution", ErrInvalidTimeline)
	}
	return nil
}

func validateEvidenceSet(evidence []EvidenceRef) error {
	seen := make(map[string]struct{}, len(evidence))
	for idx, ref := range evidence {
		if err := validateRef("evidence.kind", ref.Kind); err != nil {
			return fmt.Errorf("%w: evidence[%d]: %v", ErrInvalidEvidence, idx, err)
		}
		if err := validateRef("evidence.id", ref.ID); err != nil {
			return fmt.Errorf("%w: evidence[%d]: %v", ErrInvalidEvidence, idx, err)
		}
		if ref.Collected.IsZero() {
			return fmt.Errorf("%w: evidence[%d] collected_at is required", ErrInvalidEvidence, idx)
		}
		if !ref.Redaction.Valid() {
			return fmt.Errorf("%w: evidence[%d] has unsupported redaction state", ErrInvalidEvidence, idx)
		}
		if ref.SHA256 != "" && !sha256Pattern.MatchString(ref.SHA256) {
			return fmt.Errorf("%w: evidence[%d] sha256 must be 64 lowercase hex characters", ErrInvalidEvidence, idx)
		}
		key := ref.Kind + "\x00" + ref.ID
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate evidence reference %s/%s", ErrInvalidEvidence, ref.Kind, ref.ID)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateRef(field, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s is required and must not have surrounding whitespace", field)
	}
	if len(value) > maxReferenceLength || !refPattern.MatchString(value) {
		return fmt.Errorf("%s has invalid format", field)
	}
	return nil
}

func validateText(field, value string, maxRunes int, required bool) error {
	if required && strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must not have surrounding whitespace", field)
	}
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes {
		return fmt.Errorf("%s exceeds %d characters or is not valid UTF-8", field, maxRunes)
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\t' {
			return fmt.Errorf("%s contains control characters", field)
		}
	}
	return nil
}

// SortTimeline returns a copy sorted by timestamp, then by kind. It is intended
// for adapters that aggregate immutable timeline events before validation.
func SortTimeline(entries []TimelineEntry) []TimelineEntry {
	out := append([]TimelineEntry(nil), entries...)
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].At.Equal(out[b].At) {
			return out[a].Kind < out[b].Kind
		}
		return out[a].At.Before(out[b].At)
	})
	return out
}
