package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"control-center/internal/identity/audit"
)

var _ audit.IntegrityChecker = (*AuditLog)(nil)

func (l *AuditLog) InspectChain(ctx context.Context) (audit.IntegrityReport, error) {
	if l == nil || l.db == nil {
		return audit.IntegrityReport{}, fmt.Errorf("audit database is required")
	}
	if err := ctx.Err(); err != nil {
		return audit.IntegrityReport{}, err
	}

	rows, err := l.db.QueryContext(ctx, `SELECT id::text, occurred_at, action, outcome, actor_id::text, subject_id, host(source_ip), correlation_id, details, previous_hash, hash FROM cc_audit_events ORDER BY sequence_id ASC`)
	if err != nil {
		return audit.IntegrityReport{}, fmt.Errorf("read audit chain: %w", err)
	}
	defer rows.Close()

	previousHash := ""
	sequenceOffset := 0
	for rows.Next() {
		var event audit.Event
		var actorID, subjectID, sourceIP, correlationID, previous sql.NullString
		var details []byte
		if err := rows.Scan(&event.ID, &event.OccurredAt, &event.Action, &event.Outcome, &actorID, &subjectID, &sourceIP, &correlationID, &details, &previous, &event.Hash); err != nil {
			return audit.IntegrityReport{}, fmt.Errorf("scan audit event at offset %d: %w", sequenceOffset, err)
		}
		event.OccurredAt = event.OccurredAt.UTC().Truncate(time.Microsecond)
		event.ActorID = nullString(actorID)
		event.SubjectID = nullString(subjectID)
		event.SourceIP = nullString(sourceIP)
		event.CorrelationID = nullString(correlationID)
		event.PreviousHash = nullString(previous)
		if err := json.Unmarshal(details, &event.Details); err != nil {
			return audit.IntegrityReport{}, fmt.Errorf("decode audit details at offset %d: %w", sequenceOffset, err)
		}
		if err := audit.Verify(event, previousHash); err != nil {
			return audit.IntegrityReport{}, fmt.Errorf("verify audit event at offset %d: %w", sequenceOffset, err)
		}
		previousHash = event.Hash
		sequenceOffset++
	}
	if err := rows.Err(); err != nil {
		return audit.IntegrityReport{}, fmt.Errorf("iterate audit chain: %w", err)
	}

	return audit.IntegrityReport{EventsChecked: sequenceOffset, HeadHash: previousHash}, nil
}
