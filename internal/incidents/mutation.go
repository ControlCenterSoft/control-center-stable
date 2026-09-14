package incidents

import (
	"errors"
	"fmt"
	"math"
	"time"

	"control-center/internal/corecontracts"
)

var (
	// ErrIncidentStateConflict means an operator mutation is incompatible with
	// the incident's current lifecycle state. Callers should surface a conflict
	// instead of silently rewriting terminal history.
	ErrIncidentStateConflict = errors.New("incident state conflict")
	// ErrIncidentEvidenceConflict prevents an evidence identity from being
	// rebound to different metadata. Evidence references are append-only.
	ErrIncidentEvidenceConflict = errors.New("incident evidence conflict")
	// ErrIncidentNoChange prevents a write that would only churn resource_version.
	ErrIncidentNoChange = errors.New("incident mutation has no effective change")
)

// PreparedMutation is a side-effect-free mutation result. Storage adapters
// must apply Precondition atomically and use DesiredChanged when validating the
// metadata successor. Preparing a mutation never authorizes the actor.
type PreparedMutation struct {
	Precondition   corecontracts.ObjectPrecondition
	Next           Incident
	DesiredChanged bool
}

// AcknowledgeRequest records that an authorized operator accepted ownership of
// an open incident. Note is bounded timeline text and may be empty.
type AcknowledgeRequest struct {
	Precondition    corecontracts.ObjectPrecondition `json:"precondition"`
	ActorID         string                           `json:"actor_id"`
	OccurredAt      time.Time                        `json:"occurred_at"`
	ResourceVersion string                           `json:"resource_version"`
	Note            string                           `json:"note,omitempty"`
}

// PrepareAcknowledgement creates an acknowledged successor without mutating
// current. Authorization, MFA/step-up policy and audit emission remain adapter
// responsibilities and must complete before persistence is exposed as success.
func PrepareAcknowledgement(current Incident, request AcknowledgeRequest) (PreparedMutation, error) {
	if err := validateMutationBase(current, request.Precondition, request.ActorID, request.OccurredAt, request.ResourceVersion); err != nil {
		return PreparedMutation{}, err
	}
	if current.Status != StatusOpen {
		return PreparedMutation{}, fmt.Errorf("%w: acknowledge requires open incident, current status is %q", ErrIncidentStateConflict, current.Status)
	}
	if err := validateText("acknowledgement.note", request.Note, maxSummaryLength, false); err != nil {
		return PreparedMutation{}, fmt.Errorf("%w: %v", ErrInvalidAcknowledgement, err)
	}

	at := request.OccurredAt.UTC()
	next, err := semanticSuccessor(current, at, request.ResourceVersion)
	if err != nil {
		return PreparedMutation{}, err
	}
	next.Status = StatusAcknowledged
	next.Acknowledgement = &Acknowledgement{ActorID: request.ActorID, At: at}
	next.Timeline = append(next.Timeline, TimelineEntry{
		Kind:    TimelineAcknowledged,
		At:      at,
		ActorID: request.ActorID,
		Summary: request.Note,
	})
	if err := next.Validate(); err != nil {
		return PreparedMutation{}, err
	}
	if err := corecontracts.ValidateSuccessor(current.ObjectMetadata, next.ObjectMetadata, true); err != nil {
		return PreparedMutation{}, err
	}
	return PreparedMutation{Precondition: request.Precondition, Next: next, DesiredChanged: true}, nil
}

// ResolveRequest closes an acknowledged incident. Resolution is required and
// evidence references are copied into the terminal timeline event rather than
// embedding evidence payloads.
type ResolveRequest struct {
	Precondition    corecontracts.ObjectPrecondition `json:"precondition"`
	ActorID         string                           `json:"actor_id"`
	OccurredAt      time.Time                        `json:"occurred_at"`
	ResourceVersion string                           `json:"resource_version"`
	Resolution      string                           `json:"resolution"`
	Evidence        []EvidenceRef                    `json:"evidence,omitempty"`
}

// PrepareResolution creates a terminal resolved successor. Direct open ->
// resolved transitions are rejected so every resolution has an accountable
// acknowledgement event first.
func PrepareResolution(current Incident, request ResolveRequest) (PreparedMutation, error) {
	if err := validateMutationBase(current, request.Precondition, request.ActorID, request.OccurredAt, request.ResourceVersion); err != nil {
		return PreparedMutation{}, err
	}
	if current.Status != StatusAcknowledged {
		return PreparedMutation{}, fmt.Errorf("%w: resolve requires acknowledged incident, current status is %q", ErrIncidentStateConflict, current.Status)
	}
	if err := validateText("resolution", request.Resolution, maxSummaryLength, true); err != nil {
		return PreparedMutation{}, fmt.Errorf("%w: %v", ErrInvalidTimeline, err)
	}
	if err := validateEvidenceSet(request.Evidence); err != nil {
		return PreparedMutation{}, err
	}
	at := request.OccurredAt.UTC()
	for idx, evidence := range request.Evidence {
		if evidence.Collected.After(at) {
			return PreparedMutation{}, fmt.Errorf("%w: resolution evidence[%d] is collected after occurred_at", ErrInvalidEvidence, idx)
		}
	}

	next, err := semanticSuccessor(current, at, request.ResourceVersion)
	if err != nil {
		return PreparedMutation{}, err
	}
	next.Status = StatusResolved
	next.Timeline = append(next.Timeline, TimelineEntry{
		Kind:     TimelineResolved,
		At:       at,
		ActorID:  request.ActorID,
		Summary:  request.Resolution,
		Evidence: cloneEvidence(request.Evidence),
	})
	if err := next.Validate(); err != nil {
		return PreparedMutation{}, err
	}
	if err := corecontracts.ValidateSuccessor(current.ObjectMetadata, next.ObjectMetadata, true); err != nil {
		return PreparedMutation{}, err
	}
	return PreparedMutation{Precondition: request.Precondition, Next: next, DesiredChanged: true}, nil
}

// EvidenceUpdateRequest attaches bounded runbook/evidence metadata to a live
// incident. Existing evidence identities cannot be rewritten; exact repeats are
// idempotently ignored. Runbook nil means "leave unchanged", never "clear".
type EvidenceUpdateRequest struct {
	Precondition    corecontracts.ObjectPrecondition `json:"precondition"`
	ActorID         string                           `json:"actor_id"`
	OccurredAt      time.Time                        `json:"occurred_at"`
	ResourceVersion string                           `json:"resource_version"`
	Runbook         *RunbookRef                      `json:"runbook,omitempty"`
	Evidence        []EvidenceRef                    `json:"evidence,omitempty"`
	Note            string                           `json:"note"`
}

// PrepareEvidenceUpdate creates an append-only evidence/runbook update. It is
// intentionally unavailable after resolution so terminal incident evidence is
// immutable in the Core read model.
func PrepareEvidenceUpdate(current Incident, request EvidenceUpdateRequest) (PreparedMutation, error) {
	if err := validateMutationBase(current, request.Precondition, request.ActorID, request.OccurredAt, request.ResourceVersion); err != nil {
		return PreparedMutation{}, err
	}
	if current.Status == StatusResolved {
		return PreparedMutation{}, fmt.Errorf("%w: resolved incident metadata is immutable", ErrIncidentStateConflict)
	}
	if err := validateText("evidence_update.note", request.Note, maxSummaryLength, true); err != nil {
		return PreparedMutation{}, fmt.Errorf("%w: %v", ErrInvalidTimeline, err)
	}
	if request.Runbook != nil {
		if err := validateRef("runbook.id", request.Runbook.ID); err != nil {
			return PreparedMutation{}, fmt.Errorf("%w: %v", ErrInvalidIncident, err)
		}
		if err := validateRef("runbook.revision", request.Runbook.Revision); err != nil {
			return PreparedMutation{}, fmt.Errorf("%w: %v", ErrInvalidIncident, err)
		}
	}
	if err := validateEvidenceSet(request.Evidence); err != nil {
		return PreparedMutation{}, err
	}

	at := request.OccurredAt.UTC()
	merged, added, err := mergeEvidence(current.Evidence, request.Evidence, at)
	if err != nil {
		return PreparedMutation{}, err
	}
	runbookChanged := request.Runbook != nil && !runbookEqual(current.Runbook, request.Runbook)
	if !runbookChanged && len(added) == 0 {
		return PreparedMutation{}, ErrIncidentNoChange
	}

	next := cloneIncident(current)
	next.UpdatedAt = at
	next.ResourceVersion = request.ResourceVersion
	// Evidence/runbook annotation does not change lifecycle state. Generation
	// therefore remains stable while resource_version advances.
	if request.Runbook != nil {
		runbook := *request.Runbook
		next.Runbook = &runbook
	}
	next.Evidence = merged
	next.Timeline = append(next.Timeline, TimelineEntry{
		Kind:     TimelineNote,
		At:       at,
		ActorID:  request.ActorID,
		Summary:  request.Note,
		Evidence: cloneEvidence(added),
	})
	if err := next.Validate(); err != nil {
		return PreparedMutation{}, err
	}
	if err := corecontracts.ValidateSuccessor(current.ObjectMetadata, next.ObjectMetadata, false); err != nil {
		return PreparedMutation{}, err
	}
	return PreparedMutation{Precondition: request.Precondition, Next: next, DesiredChanged: false}, nil
}

func validateMutationBase(current Incident, precondition corecontracts.ObjectPrecondition, actorID string, occurredAt time.Time, resourceVersion string) error {
	if err := current.Validate(); err != nil {
		return err
	}
	if err := precondition.ValidateAgainst(current.ObjectMetadata); err != nil {
		return err
	}
	if err := validateRef("actor_id", actorID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAcknowledgement, err)
	}
	if occurredAt.IsZero() {
		return fmt.Errorf("%w: occurred_at is required", ErrInvalidTimeline)
	}
	occurredAt = occurredAt.UTC()
	if occurredAt.Before(current.UpdatedAt) {
		return fmt.Errorf("%w: occurred_at precedes current updated_at", ErrIncidentStateConflict)
	}
	if resourceVersion == "" || resourceVersion == current.ResourceVersion {
		return fmt.Errorf("%w: new resource_version is required", corecontracts.ErrInvalidTransition)
	}
	return nil
}

func semanticSuccessor(current Incident, at time.Time, resourceVersion string) (Incident, error) {
	if current.Generation == math.MaxUint64 {
		return Incident{}, fmt.Errorf("%w: generation overflow", corecontracts.ErrInvalidTransition)
	}
	next := cloneIncident(current)
	next.Generation++
	next.ResourceVersion = resourceVersion
	next.UpdatedAt = at.UTC()
	return next, nil
}

func mergeEvidence(existing, requested []EvidenceRef, occurredAt time.Time) ([]EvidenceRef, []EvidenceRef, error) {
	merged := cloneEvidence(existing)
	added := make([]EvidenceRef, 0, len(requested))
	for idx, candidate := range requested {
		if candidate.Collected.After(occurredAt) {
			return nil, nil, fmt.Errorf("%w: evidence[%d] is collected after occurred_at", ErrInvalidEvidence, idx)
		}
		matched := false
		for _, current := range merged {
			if current.Kind != candidate.Kind || current.ID != candidate.ID {
				continue
			}
			matched = true
			if !evidenceEqual(current, candidate) {
				return nil, nil, fmt.Errorf("%w: evidence identity %s/%s already has different metadata", ErrIncidentEvidenceConflict, candidate.Kind, candidate.ID)
			}
			break
		}
		if !matched {
			merged = append(merged, candidate)
			added = append(added, candidate)
		}
	}
	if len(merged) > maxEvidenceRefs {
		return nil, nil, fmt.Errorf("%w: too many incident evidence references", ErrInvalidEvidence)
	}
	return merged, added, nil
}

func evidenceEqual(a, b EvidenceRef) bool {
	return a.Kind == b.Kind && a.ID == b.ID && a.SHA256 == b.SHA256 && a.Collected.Equal(b.Collected) && a.Redaction == b.Redaction
}

func runbookEqual(a, b *RunbookRef) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.ID == b.ID && a.Revision == b.Revision
}

func cloneIncident(current Incident) Incident {
	next := current
	next.AffectedResources = append([]ResourceRef(nil), current.AffectedResources...)
	next.Signals = append([]Signal(nil), current.Signals...)
	for idx := range next.Signals {
		next.Signals[idx].Evidence = cloneEvidence(current.Signals[idx].Evidence)
	}
	next.Timeline = append([]TimelineEntry(nil), current.Timeline...)
	for idx := range next.Timeline {
		next.Timeline[idx].Evidence = cloneEvidence(current.Timeline[idx].Evidence)
	}
	next.Evidence = cloneEvidence(current.Evidence)
	if current.Runbook != nil {
		runbook := *current.Runbook
		next.Runbook = &runbook
	}
	if current.Acknowledgement != nil {
		acknowledgement := *current.Acknowledgement
		next.Acknowledgement = &acknowledgement
	}
	return next
}

func cloneEvidence(input []EvidenceRef) []EvidenceRef {
	return append([]EvidenceRef(nil), input...)
}
