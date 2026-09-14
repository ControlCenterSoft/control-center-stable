package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"control-center/internal/corecontracts"
	"control-center/internal/incidents"
)

var ErrIncidentConflict = errors.New("incident persistence conflict")

// IncidentReadRepository persists the bounded 0.32 incident read model. The
// complete validated document is stored as JSONB while query-critical fields
// and affected resource references are mirrored into constrained columns.
type IncidentReadRepository struct{ db *sql.DB }

func NewIncidentReadRepository(db *sql.DB) (*IncidentReadRepository, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	return &IncidentReadRepository{db: db}, nil
}

// Create inserts one incident and its affected-resource index atomically.
func (r *IncidentReadRepository) Create(ctx context.Context, incident incidents.Incident) error {
	canonicalizeIncidentTimes(&incident)
	if err := validateIncidentForPersistence(incident); err != nil {
		return err
	}
	document, err := json.Marshal(incident)
	if err != nil {
		return fmt.Errorf("marshal incident: %w", err)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
INSERT INTO cc_incident_read_models (
    object_id,scope_id,owner_scope,generation,resource_version,
    severity,status,title,started_at,last_observed_at,document,created_at,updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		incident.ObjectID,
		incident.ScopeID,
		incident.OwnerScope,
		incident.Generation,
		incident.ResourceVersion,
		string(incident.Severity),
		string(incident.Status),
		incident.Title,
		incident.StartedAt,
		incident.LastObservedAt,
		document,
		incident.CreatedAt,
		incident.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrIncidentConflict
		}
		return err
	}
	if err := replaceIncidentResources(ctx, tx, incident); err != nil {
		return err
	}
	return tx.Commit()
}

// Replace performs a locked compare-and-swap update. desiredChanged controls
// ObjectMetadata generation semantics; callers cannot bypass metadata checks by
// writing the JSONB document directly.
func (r *IncidentReadRepository) Replace(
	ctx context.Context,
	precondition corecontracts.ObjectPrecondition,
	next incidents.Incident,
	desiredChanged bool,
) error {
	canonicalizeIncidentTimes(&next)
	if err := validateIncidentForPersistence(next); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	current, err := getIncidentTx(ctx, tx, next.ObjectID, true)
	if err != nil {
		return err
	}
	if err := precondition.ValidateAgainst(current.ObjectMetadata); err != nil {
		return err
	}
	if err := corecontracts.ValidateSuccessor(current.ObjectMetadata, next.ObjectMetadata, desiredChanged); err != nil {
		return err
	}
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
	if err := replaceIncidentResources(ctx, tx, next); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *IncidentReadRepository) Get(ctx context.Context, objectID string) (incidents.Incident, error) {
	if strings.TrimSpace(objectID) == "" || objectID != strings.TrimSpace(objectID) {
		return incidents.Incident{}, incidents.ErrInvalidIncident
	}
	return scanIncidentMirror(r.db.QueryRowContext(ctx, incidentSelect+` WHERE object_id=$1`, objectID))
}

// List executes the canonical newest-first bounded query defined by
// incidents.ListQuery. It fetches one extra row to produce a trustworthy
// continuation cursor without an unbounded count query.
func (r *IncidentReadRepository) List(ctx context.Context, query incidents.ListQuery) (incidents.ListPage, error) {
	normalized, err := incidents.NormalizeListQuery(query)
	if err != nil {
		return incidents.ListPage{}, err
	}
	statement, args := buildIncidentListSQL(normalized)
	rows, err := r.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return incidents.ListPage{}, err
	}
	defer rows.Close()

	items := make([]incidents.Incident, 0, normalized.Limit+1)
	for rows.Next() {
		incident, err := scanIncidentMirror(rows)
		if err != nil {
			return incidents.ListPage{}, err
		}
		items = append(items, incident)
	}
	if err := rows.Err(); err != nil {
		return incidents.ListPage{}, err
	}

	hasMore := len(items) > normalized.Limit
	if hasMore {
		items = items[:normalized.Limit]
	}
	page := incidents.ListPage{Items: items, HasMore: hasMore}
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		page.Next = &incidents.ListCursor{StartedAt: last.StartedAt.UTC(), ObjectID: last.ObjectID}
	}
	return page, nil
}

const incidentSelect = `
SELECT object_id,scope_id,owner_scope,generation,resource_version,
       severity,status,title,started_at,last_observed_at,document,created_at,updated_at
FROM cc_incident_read_models`

type incidentScanner interface{ Scan(...any) error }

func scanIncidentMirror(row incidentScanner) (incidents.Incident, error) {
	var (
		objectID, scopeID, ownerScope, resourceVersion string
		severity, status, title                        string
		generation                                     int64
		startedAt, lastObservedAt                      sql.NullTime
		createdAt, updatedAt                           sql.NullTime
		document                                       []byte
	)
	if err := row.Scan(
		&objectID,
		&scopeID,
		&ownerScope,
		&generation,
		&resourceVersion,
		&severity,
		&status,
		&title,
		&startedAt,
		&lastObservedAt,
		&document,
		&createdAt,
		&updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return incidents.Incident{}, incidents.ErrNotFound
		}
		return incidents.Incident{}, err
	}
	var incident incidents.Incident
	if err := json.Unmarshal(document, &incident); err != nil {
		return incidents.Incident{}, fmt.Errorf("decode incident %q: %w", objectID, err)
	}
	if err := incident.Validate(); err != nil {
		return incidents.Incident{}, fmt.Errorf("stored incident %q: %w", objectID, err)
	}
	if generation <= 0 ||
		incident.ObjectID != objectID ||
		incident.ScopeID != scopeID ||
		incident.OwnerScope != ownerScope ||
		incident.Generation != uint64(generation) ||
		incident.ResourceVersion != resourceVersion ||
		string(incident.Severity) != severity ||
		string(incident.Status) != status ||
		incident.Title != title ||
		!startedAt.Valid || !incident.StartedAt.Equal(startedAt.Time) ||
		!lastObservedAt.Valid || !incident.LastObservedAt.Equal(lastObservedAt.Time) ||
		!createdAt.Valid || !incident.CreatedAt.Equal(createdAt.Time) ||
		!updatedAt.Valid || !incident.UpdatedAt.Equal(updatedAt.Time) {
		return incidents.Incident{}, fmt.Errorf("%w: indexed columns differ from incident document %q", ErrIncidentConflict, objectID)
	}
	return incident, nil
}

func getIncidentTx(ctx context.Context, tx *sql.Tx, objectID string, lock bool) (incidents.Incident, error) {
	query := incidentSelect + ` WHERE object_id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	return scanIncidentMirror(tx.QueryRowContext(ctx, query, objectID))
}

func validateIncidentForPersistence(incident incidents.Incident) error {
	if err := incident.Validate(); err != nil {
		return err
	}
	if incident.Generation > uint64(math.MaxInt64) {
		return fmt.Errorf("%w: generation exceeds PostgreSQL bigint", incidents.ErrInvalidIncident)
	}
	return nil
}

// PostgreSQL timestamptz has microsecond precision. Canonicalizing every time
// in the document before validation/serialization prevents a nested timeline or
// evidence timestamp from moving past a rounded object-lifetime boundary.
func canonicalizeIncidentTimes(incident *incidents.Incident) {
	incident.CreatedAt = postgresTime(incident.CreatedAt)
	incident.UpdatedAt = postgresTime(incident.UpdatedAt)
	incident.StartedAt = postgresTime(incident.StartedAt)
	incident.LastObservedAt = postgresTime(incident.LastObservedAt)
	if incident.Acknowledgement != nil {
		incident.Acknowledgement.At = postgresTime(incident.Acknowledgement.At)
	}
	for idx := range incident.Signals {
		incident.Signals[idx].ObservedAt = postgresTime(incident.Signals[idx].ObservedAt)
		for evidence := range incident.Signals[idx].Evidence {
			incident.Signals[idx].Evidence[evidence].Collected = postgresTime(incident.Signals[idx].Evidence[evidence].Collected)
		}
	}
	for idx := range incident.Timeline {
		incident.Timeline[idx].At = postgresTime(incident.Timeline[idx].At)
		for evidence := range incident.Timeline[idx].Evidence {
			incident.Timeline[idx].Evidence[evidence].Collected = postgresTime(incident.Timeline[idx].Evidence[evidence].Collected)
		}
	}
	for idx := range incident.Evidence {
		incident.Evidence[idx].Collected = postgresTime(incident.Evidence[idx].Collected)
	}
}

func postgresTime(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

func replaceIncidentResources(ctx context.Context, tx *sql.Tx, incident incidents.Incident) error {
	for _, resource := range incident.AffectedResources {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO cc_incident_affected_resources (incident_id,resource_kind,resource_id,scope_id)
VALUES ($1,$2,$3,$4)`, incident.ObjectID, resource.Kind, resource.ID, resource.ScopeID); err != nil {
			return err
		}
	}
	return nil
}

func buildIncidentListSQL(query incidents.ListQuery) (string, []any) {
	var builder strings.Builder
	builder.WriteString(incidentSelect)
	builder.WriteString(` WHERE TRUE`)
	args := make([]any, 0, 16)
	bind := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}

	if query.ScopeID != "" {
		builder.WriteString(` AND scope_id=`)
		builder.WriteString(bind(query.ScopeID))
	}
	if len(query.Statuses) > 0 {
		builder.WriteString(` AND status IN (`)
		for idx, value := range query.Statuses {
			if idx > 0 {
				builder.WriteByte(',')
			}
			builder.WriteString(bind(string(value)))
		}
		builder.WriteByte(')')
	}
	if len(query.Severities) > 0 {
		builder.WriteString(` AND severity IN (`)
		for idx, value := range query.Severities {
			if idx > 0 {
				builder.WriteByte(',')
			}
			builder.WriteString(bind(string(value)))
		}
		builder.WriteByte(')')
	}
	if query.StartedFrom != nil {
		builder.WriteString(` AND started_at>=`)
		builder.WriteString(bind(query.StartedFrom.UTC()))
	}
	if query.StartedBefore != nil {
		builder.WriteString(` AND started_at<`)
		builder.WriteString(bind(query.StartedBefore.UTC()))
	}
	if query.Before != nil {
		started := bind(query.Before.StartedAt.UTC())
		objectID := bind(query.Before.ObjectID)
		builder.WriteString(` AND (started_at<`)
		builder.WriteString(started)
		builder.WriteString(` OR (started_at=`)
		builder.WriteString(started)
		builder.WriteString(` AND object_id>`)
		builder.WriteString(objectID)
		builder.WriteString(`))`)
	}
	if query.ResourceKind != "" {
		kind := bind(query.ResourceKind)
		builder.WriteString(` AND EXISTS (SELECT 1 FROM cc_incident_affected_resources r WHERE r.incident_id=cc_incident_read_models.object_id AND r.resource_kind=`)
		builder.WriteString(kind)
		if query.ResourceID != "" {
			builder.WriteString(` AND r.resource_id=`)
			builder.WriteString(bind(query.ResourceID))
		}
		builder.WriteByte(')')
	}
	builder.WriteString(` ORDER BY started_at DESC, object_id ASC LIMIT `)
	builder.WriteString(bind(query.Limit + 1))
	return builder.String(), args
}

var _ incidents.Reader = (*IncidentReadRepository)(nil)
