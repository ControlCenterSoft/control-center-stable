package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"control-center/internal/corecontracts"
	"control-center/internal/identity/audit"
	"control-center/internal/incidents"
)

// IncidentMutationCommitter is the PostgreSQL durability boundary required by
// incidents.OperatorService. The incident successor and its immutable security
// audit event are committed in one database transaction; neither is reported as
// successful unless both writes are durable. The returned incident is the
// canonical representation actually written to PostgreSQL.
type IncidentMutationCommitter struct {
	db *sql.DB
}

func NewIncidentMutationCommitter(db *sql.DB) (*IncidentMutationCommitter, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	return &IncidentMutationCommitter{db: db}, nil
}

func (c *IncidentMutationCommitter) CommitIncidentMutation(ctx context.Context, prepared incidents.PreparedMutation, record incidents.OperatorAuditRecord) (incidents.Incident, error) {
	if c == nil || c.db == nil {
		return incidents.Incident{}, incidents.ErrOperatorDependencyUnavailable
	}
	if err := ctx.Err(); err != nil {
		return incidents.Incident{}, err
	}

	next := prepared.Next
	canonicalizeIncidentTimes(&next)
	if err := validateIncidentForPersistence(next); err != nil {
		return incidents.Incident{}, err
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return incidents.Incident{}, err
	}
	defer func() { _ = tx.Rollback() }()

	current, err := getIncidentTx(ctx, tx, next.ObjectID, true)
	if err != nil {
		return incidents.Incident{}, err
	}
	if err := prepared.Precondition.ValidateAgainst(current.ObjectMetadata); err != nil {
		return incidents.Incident{}, err
	}
	if err := corecontracts.ValidateSuccessor(current.ObjectMetadata, next.ObjectMetadata, prepared.DesiredChanged); err != nil {
		return incidents.Incident{}, err
	}

	event, err := incidentOperatorAuditEvent(current, next, record)
	if err != nil {
		return incidents.Incident{}, err
	}
	if err := replaceIncidentWithinTx(ctx, tx, current, next); err != nil {
		return incidents.Incident{}, err
	}
	if err := appendAuditEventWithinTx(ctx, tx, event); err != nil {
		return incidents.Incident{}, err
	}
	if err := tx.Commit(); err != nil {
		return incidents.Incident{}, err
	}
	return next, nil
}

func replaceIncidentWithinTx(ctx context.Context, tx *sql.Tx, current, next incidents.Incident) error {
	document, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("marshal incident: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
UPDATE cc_incident_read_models
SET scope_id=$2,
    owner_scope=$3,
    generation=$4,
    resource_version=$5,
    severity=$6,
    status=$7,
    title=$8,
    started_at=$9,
    last_observed_at=$10,
    document=$11,
    created_at=$12,
    updated_at=$13
WHERE object_id=$1 AND resource_version=$14`,
		next.ObjectID,
		next.ScopeID,
		next.OwnerScope,
		next.Generation,
		next.ResourceVersion,
		string(next.Severity),
		string(next.Status),
		next.Title,
		next.StartedAt,
		next.LastObservedAt,
		document,
		next.CreatedAt,
		next.UpdatedAt,
		current.ResourceVersion,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrIncidentConflict
		}
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return corecontracts.ErrPreconditionFailed
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM cc_incident_affected_resources WHERE incident_id=$1`, next.ObjectID); err != nil {
		return err
	}
	return replaceIncidentResources(ctx, tx, next)
}

func incidentOperatorAuditEvent(current, next incidents.Incident, record incidents.OperatorAuditRecord) (audit.Event, error) {
	if strings.TrimSpace(record.ActorID) == "" || record.OccurredAt.IsZero() {
		return audit.Event{}, errors.New("incident operator audit actor and timestamp are required")
	}
	if record.Action != incidents.CapabilityIncidentAcknowledge &&
		record.Action != incidents.CapabilityIncidentResolve &&
		record.Action != incidents.CapabilityIncidentEvidenceUpdate {
		return audit.Event{}, fmt.Errorf("unsupported incident operator audit action %q", record.Action)
	}
	if record.IncidentID != current.ObjectID || record.IncidentID != next.ObjectID ||
		record.ScopeID != current.ScopeID || record.ScopeID != next.ScopeID {
		return audit.Event{}, errors.New("incident operator audit identity binding mismatch")
	}
	if record.BeforeStatus != current.Status || record.AfterStatus != next.Status ||
		record.BeforeGeneration != current.Generation || record.AfterGeneration != next.Generation ||
		record.BeforeResourceVersion != current.ResourceVersion || record.AfterResourceVersion != next.ResourceVersion {
		return audit.Event{}, errors.New("incident operator audit revision binding mismatch")
	}

	return audit.Event{
		OccurredAt: record.OccurredAt,
		Action:     string(record.Action),
		Outcome:    "success",
		ActorID:    record.ActorID,
		SubjectID:  record.IncidentID,
		Details: map[string]any{
			"scope_id":                record.ScopeID,
			"before_status":           record.BeforeStatus,
			"after_status":            record.AfterStatus,
			"before_generation":       record.BeforeGeneration,
			"after_generation":        record.AfterGeneration,
			"before_resource_version": record.BeforeResourceVersion,
			"after_resource_version":  record.AfterResourceVersion,
		},
	}, nil
}

func appendAuditEventWithinTx(ctx context.Context, tx *sql.Tx, event audit.Event) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('control-center:audit-chain'))`); err != nil {
		return err
	}
	var previous sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT hash FROM cc_audit_events ORDER BY sequence_id DESC LIMIT 1`).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	prepared, err := audit.Prepare(event, previous.String)
	if err != nil {
		return err
	}
	details, err := json.Marshal(prepared.Details)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO cc_audit_events (
    id,occurred_at,action,outcome,actor_id,subject_id,source_ip,
    correlation_id,details,previous_hash,hash
) VALUES (
    $1::uuid,$2,$3,$4,NULLIF($5,'')::uuid,NULLIF($6,''),NULLIF($7,'')::inet,
    NULLIF($8,''),$9::jsonb,NULLIF($10,''),$11
)`, prepared.ID, prepared.OccurredAt, prepared.Action, prepared.Outcome, prepared.ActorID,
		prepared.SubjectID, prepared.SourceIP, prepared.CorrelationID, string(details), prepared.PreviousHash, prepared.Hash)
	return err
}

var _ incidents.AtomicMutationCommitter = (*IncidentMutationCommitter)(nil)
