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
