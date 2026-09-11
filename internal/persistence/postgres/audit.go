package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"control-center/internal/identity/audit"
)

type AuditLog struct{ db *sql.DB }

func NewAuditLog(db *sql.DB) (*AuditLog, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	return &AuditLog{db: db}, nil
}
func (l *AuditLog) Append(ctx context.Context, event audit.Event) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('control-center:audit-chain'))`); err != nil {
		return err
	}
	var previous sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT hash FROM cc_audit_events ORDER BY sequence_id DESC LIMIT 1`).Scan(&previous)
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
	_, err = tx.ExecContext(ctx, `INSERT INTO cc_audit_events (id,occurred_at,action,outcome,actor_id,subject_id,source_ip,correlation_id,details,previous_hash,hash) VALUES ($1::uuid,$2,$3,$4,NULLIF($5,'')::uuid,NULLIF($6,''),NULLIF($7,'')::inet,NULLIF($8,''),$9::jsonb,NULLIF($10,''),$11)`, prepared.ID, prepared.OccurredAt, prepared.Action, prepared.Outcome, prepared.ActorID, prepared.SubjectID, prepared.SourceIP, prepared.CorrelationID, string(details), prepared.PreviousHash, prepared.Hash)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (l *AuditLog) Read(ctx context.Context, query audit.Query) (audit.Page, error) {
	query, err := audit.NormalizeQuery(query)
	if err != nil {
		return audit.Page{}, err
	}
	var before any
	if query.BeforeSequenceID > 0 {
		before = query.BeforeSequenceID
	}
	rows, err := l.db.QueryContext(ctx, `
SELECT sequence_id, id::text, occurred_at, action, outcome, actor_id::text, subject_id, host(source_ip), correlation_id, details, previous_hash, hash
FROM cc_audit_events
WHERE ($1::bigint IS NULL OR sequence_id < $1)
  AND ($2::text = '' OR action = $2)
  AND ($3::text = '' OR outcome = $3)
  AND ($4::text = '' OR actor_id::text = $4)
  AND ($5::text = '' OR subject_id = $5)
ORDER BY sequence_id DESC
LIMIT $6`, before, query.Action, query.Outcome, query.ActorID, query.SubjectID, query.Limit+1)
	if err != nil {
		return audit.Page{}, fmt.Errorf("read audit events: %w", err)
	}
	defer rows.Close()

	entries := make([]audit.Entry, 0, query.Limit+1)
	for rows.Next() {
		var entry audit.Entry
		var actorID, subjectID, sourceIP, correlationID, previous sql.NullString
		var details []byte
		if err := rows.Scan(&entry.SequenceID, &entry.Event.ID, &entry.Event.OccurredAt, &entry.Event.Action, &entry.Event.Outcome, &actorID, &subjectID, &sourceIP, &correlationID, &details, &previous, &entry.Event.Hash); err != nil {
			return audit.Page{}, fmt.Errorf("scan audit event: %w", err)
		}
		entry.Event.OccurredAt = entry.Event.OccurredAt.UTC().Truncate(time.Microsecond)
		entry.Event.ActorID = nullString(actorID)
		entry.Event.SubjectID = nullString(subjectID)
		entry.Event.SourceIP = nullString(sourceIP)
		entry.Event.CorrelationID = nullString(correlationID)
		entry.Event.PreviousHash = nullString(previous)
		if err := json.Unmarshal(details, &entry.Event.Details); err != nil {
			return audit.Page{}, fmt.Errorf("decode audit details for sequence %d: %w", entry.SequenceID, err)
		}
		if err := audit.Verify(entry.Event, entry.Event.PreviousHash); err != nil {
			return audit.Page{}, fmt.Errorf("verify audit event at sequence %d: %w", entry.SequenceID, err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return audit.Page{}, fmt.Errorf("iterate audit events: %w", err)
	}
	hasMore := len(entries) > query.Limit
	if hasMore {
		entries = entries[:query.Limit]
	}
	return audit.Page{Entries: entries, HasMore: hasMore}, nil
}

func (l *AuditLog) VerifyChain(ctx context.Context) error {
	rows, err := l.db.QueryContext(ctx, `SELECT id::text, occurred_at, action, outcome, actor_id::text, subject_id, host(source_ip), correlation_id, details, previous_hash, hash FROM cc_audit_events ORDER BY sequence_id ASC`)
	if err != nil {
		return fmt.Errorf("read audit chain: %w", err)
	}
	defer rows.Close()
	previousHash := ""
	sequenceOffset := 0
	for rows.Next() {
		var event audit.Event
		var actorID, subjectID, sourceIP, correlationID, previous sql.NullString
		var details []byte
		if err := rows.Scan(&event.ID, &event.OccurredAt, &event.Action, &event.Outcome, &actorID, &subjectID, &sourceIP, &correlationID, &details, &previous, &event.Hash); err != nil {
			return fmt.Errorf("scan audit event at offset %d: %w", sequenceOffset, err)
		}
		event.OccurredAt = event.OccurredAt.UTC().Truncate(time.Microsecond)
		event.ActorID = nullString(actorID)
		event.SubjectID = nullString(subjectID)
		event.SourceIP = nullString(sourceIP)
		event.CorrelationID = nullString(correlationID)
		event.PreviousHash = nullString(previous)
		if err := json.Unmarshal(details, &event.Details); err != nil {
			return fmt.Errorf("decode audit details at offset %d: %w", sequenceOffset, err)
		}
		if err := audit.Verify(event, previousHash); err != nil {
			return fmt.Errorf("verify audit event at offset %d: %w", sequenceOffset, err)
		}
		previousHash = event.Hash
		sequenceOffset++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate audit chain: %w", err)
	}
	return nil
}
func nullString(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}
