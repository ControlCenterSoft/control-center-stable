package incidents

import (
	"errors"
	"strings"
	"testing"
	"time"

	"control-center/internal/corecontracts"
)

func TestPrepareAcknowledgementBuildsSemanticSuccessor(t *testing.T) {
	current := validIncident(StatusOpen)
	at := current.UpdatedAt.Add(time.Minute)
	request := AcknowledgeRequest{
		Precondition:    incidentPrecondition(current),
		ActorID:         "user:operator-1",
		OccurredAt:      at,
		ResourceVersion: "rv-ack-3",
		Note:            "Принято в работу дежурным оператором",
	}

	prepared, err := PrepareAcknowledgement(current, request)
	if err != nil {
		t.Fatalf("PrepareAcknowledgement() error = %v", err)
	}
	if !prepared.DesiredChanged {
		t.Fatal("DesiredChanged = false, want true")
	}
	if prepared.Next.Status != StatusAcknowledged {
		t.Fatalf("status = %q, want %q", prepared.Next.Status, StatusAcknowledged)
	}
	if prepared.Next.Generation != current.Generation+1 {
		t.Fatalf("generation = %d, want %d", prepared.Next.Generation, current.Generation+1)
	}
	if prepared.Next.ResourceVersion != request.ResourceVersion || !prepared.Next.UpdatedAt.Equal(at) {
		t.Fatalf("metadata = %#v", prepared.Next.ObjectMetadata)
	}
	if prepared.Next.Acknowledgement == nil || prepared.Next.Acknowledgement.ActorID != request.ActorID || !prepared.Next.Acknowledgement.At.Equal(at) {
		t.Fatalf("acknowledgement = %#v", prepared.Next.Acknowledgement)
	}
	last := prepared.Next.Timeline[len(prepared.Next.Timeline)-1]
	if last.Kind != TimelineAcknowledged || last.ActorID != request.ActorID || last.Summary != request.Note {
		t.Fatalf("last timeline event = %#v", last)
	}
	if err := prepared.Next.Validate(); err != nil {
		t.Fatalf("prepared incident invalid: %v", err)
	}
	if current.Status != StatusOpen || current.Acknowledgement != nil || len(current.Timeline) != 2 {
		t.Fatalf("PrepareAcknowledgement mutated current: %#v", current)
	}
}

func TestPrepareAcknowledgementRejectsStalePrecondition(t *testing.T) {
	current := validIncident(StatusOpen)
	request := AcknowledgeRequest{
		Precondition: corecontracts.ObjectPrecondition{
			ObjectID:        current.ObjectID,
			ResourceVersion: "stale-rv",
		},
		ActorID:         "user:operator-1",
		OccurredAt:      current.UpdatedAt.Add(time.Minute),
		ResourceVersion: "rv-next",
	}
	if _, err := PrepareAcknowledgement(current, request); !errors.Is(err, corecontracts.ErrPreconditionFailed) {
		t.Fatalf("PrepareAcknowledgement() error = %v, want ErrPreconditionFailed", err)
	}
}

func TestPrepareResolutionRequiresAcknowledgement(t *testing.T) {
	current := validIncident(StatusOpen)
	request := ResolveRequest{
		Precondition:    incidentPrecondition(current),
		ActorID:         "user:operator-1",
		OccurredAt:      current.UpdatedAt.Add(time.Minute),
		ResourceVersion: "rv-resolved",
		Resolution:      "Connectivity verification freshness recovered",
	}
	if _, err := PrepareResolution(current, request); !errors.Is(err, ErrIncidentStateConflict) {
		t.Fatalf("PrepareResolution() error = %v, want ErrIncidentStateConflict", err)
	}
}

func TestPrepareResolutionBuildsTerminalSuccessor(t *testing.T) {
	current := validIncident(StatusAcknowledged)
	at := current.UpdatedAt.Add(time.Minute)
	evidence := EvidenceRef{
		Kind:      "health-snapshot",
		ID:        "snapshot:post-recovery",
		SHA256:    strings.Repeat("b", 64),
		Collected: at.Add(-time.Second),
		Redaction: RedactionApplied,
	}
	request := ResolveRequest{
		Precondition:    incidentPrecondition(current),
		ActorID:         "user:operator-2",
		OccurredAt:      at,
		ResourceVersion: "rv-resolved-3",
		Resolution:      "Проверка связности снова укладывается в freshness budget",
		Evidence:        []EvidenceRef{evidence},
	}

	prepared, err := PrepareResolution(current, request)
	if err != nil {
		t.Fatalf("PrepareResolution() error = %v", err)
	}
	if prepared.Next.Status != StatusResolved || !prepared.DesiredChanged {
		t.Fatalf("prepared = %#v", prepared)
	}
	if prepared.Next.Generation != current.Generation+1 {
		t.Fatalf("generation = %d, want %d", prepared.Next.Generation, current.Generation+1)
	}
	last := prepared.Next.Timeline[len(prepared.Next.Timeline)-1]
	if last.Kind != TimelineResolved || last.ActorID != request.ActorID || len(last.Evidence) != 1 || last.Evidence[0].ID != evidence.ID {
		t.Fatalf("terminal event = %#v", last)
	}
	if err := prepared.Next.Validate(); err != nil {
		t.Fatalf("prepared incident invalid: %v", err)
	}
}

func TestPrepareResolutionRejectsFutureEvidence(t *testing.T) {
	current := validIncident(StatusAcknowledged)
	at := current.UpdatedAt.Add(time.Minute)
	request := ResolveRequest{
		Precondition:    incidentPrecondition(current),
		ActorID:         "user:operator-2",
		OccurredAt:      at,
		ResourceVersion: "rv-resolved-3",
		Resolution:      "Recovered",
		Evidence: []EvidenceRef{{
			Kind: "health-snapshot", ID: "snapshot:future", Collected: at.Add(time.Second), Redaction: RedactionNotApplicable,
		}},
	}
	if _, err := PrepareResolution(current, request); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("PrepareResolution() error = %v, want ErrInvalidEvidence", err)
	}
}

func TestPrepareEvidenceUpdateIsAppendOnlyAndGenerationStable(t *testing.T) {
	current := validIncident(StatusAcknowledged)
	at := current.UpdatedAt.Add(time.Minute)
	newEvidence := EvidenceRef{
		Kind:      "job-result",
		ID:        "job:verification-42",
		SHA256:    strings.Repeat("c", 64),
		Collected: at,
		Redaction: RedactionApplied,
	}
	request := EvidenceUpdateRequest{
		Precondition:    incidentPrecondition(current),
		ActorID:         "user:operator-1",
		OccurredAt:      at,
		ResourceVersion: "rv-evidence-3",
		Runbook:         &RunbookRef{ID: "runbook:network-verification", Revision: "v2"},
		Evidence:        []EvidenceRef{current.Evidence[0], newEvidence},
		Note:            "Добавлены результаты повторной проверки",
	}

	prepared, err := PrepareEvidenceUpdate(current, request)
	if err != nil {
		t.Fatalf("PrepareEvidenceUpdate() error = %v", err)
	}
	if prepared.DesiredChanged {
		t.Fatal("DesiredChanged = true, want false for evidence annotation")
	}
	if prepared.Next.Generation != current.Generation {
		t.Fatalf("generation = %d, want %d", prepared.Next.Generation, current.Generation)
	}
	if len(prepared.Next.Evidence) != len(current.Evidence)+1 {
		t.Fatalf("evidence count = %d, want %d", len(prepared.Next.Evidence), len(current.Evidence)+1)
	}
	if prepared.Next.Runbook == nil || prepared.Next.Runbook.Revision != "v2" {
		t.Fatalf("runbook = %#v", prepared.Next.Runbook)
	}
	last := prepared.Next.Timeline[len(prepared.Next.Timeline)-1]
	if last.Kind != TimelineNote || len(last.Evidence) != 1 || last.Evidence[0].ID != newEvidence.ID {
		t.Fatalf("timeline note = %#v", last)
	}
	if err := prepared.Next.Validate(); err != nil {
		t.Fatalf("prepared incident invalid: %v", err)
	}
	if current.Runbook == nil || current.Runbook.Revision != "v1" || len(current.Evidence) != 1 {
		t.Fatalf("PrepareEvidenceUpdate mutated current: %#v", current)
	}
}

func TestPrepareEvidenceUpdateRejectsEvidenceIdentityRewrite(t *testing.T) {
	current := validIncident(StatusAcknowledged)
	conflicting := current.Evidence[0]
	conflicting.SHA256 = strings.Repeat("d", 64)
	request := EvidenceUpdateRequest{
		Precondition:    incidentPrecondition(current),
		ActorID:         "user:operator-1",
		OccurredAt:      current.UpdatedAt.Add(time.Minute),
		ResourceVersion: "rv-evidence-3",
		Evidence:        []EvidenceRef{conflicting},
		Note:            "Попытка заменить evidence metadata",
	}
	if _, err := PrepareEvidenceUpdate(current, request); !errors.Is(err, ErrIncidentEvidenceConflict) {
		t.Fatalf("PrepareEvidenceUpdate() error = %v, want ErrIncidentEvidenceConflict", err)
	}
}

func TestPrepareEvidenceUpdateRejectsResolvedIncident(t *testing.T) {
	current := validIncident(StatusResolved)
	request := EvidenceUpdateRequest{
		Precondition:    incidentPrecondition(current),
		ActorID:         "user:operator-1",
		OccurredAt:      current.UpdatedAt.Add(time.Minute),
		ResourceVersion: "rv-evidence-4",
		Runbook:         &RunbookRef{ID: "runbook:network-verification", Revision: "v2"},
		Note:            "Late metadata change",
	}
	if _, err := PrepareEvidenceUpdate(current, request); !errors.Is(err, ErrIncidentStateConflict) {
		t.Fatalf("PrepareEvidenceUpdate() error = %v, want ErrIncidentStateConflict", err)
	}
}

func TestPrepareEvidenceUpdateRejectsNoOp(t *testing.T) {
	current := validIncident(StatusAcknowledged)
	request := EvidenceUpdateRequest{
		Precondition:    incidentPrecondition(current),
		ActorID:         "user:operator-1",
		OccurredAt:      current.UpdatedAt.Add(time.Minute),
		ResourceVersion: "rv-evidence-3",
		Runbook:         current.Runbook,
		Evidence:        []EvidenceRef{current.Evidence[0]},
		Note:            "No semantic change",
	}
	if _, err := PrepareEvidenceUpdate(current, request); !errors.Is(err, ErrIncidentNoChange) {
		t.Fatalf("PrepareEvidenceUpdate() error = %v, want ErrIncidentNoChange", err)
	}
}

func incidentPrecondition(incident Incident) corecontracts.ObjectPrecondition {
	generation := incident.Generation
	return corecontracts.ObjectPrecondition{
		ObjectID:        incident.ObjectID,
		ResourceVersion: incident.ResourceVersion,
		Generation:      &generation,
	}
}
