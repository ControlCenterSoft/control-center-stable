package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"control-center/internal/orchestration/change"
	orchestrationconfig "control-center/internal/orchestration/config"
	orchestrationapi "control-center/internal/orchestration/httpapi"
	"control-center/internal/orchestration/job"
	"control-center/internal/orchestration/policy"
)

type OrchestrationState struct{ db *sql.DB }

func NewOrchestrationState(db *sql.DB) (*OrchestrationState, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	return &OrchestrationState{db: db}, nil
}

func (s *OrchestrationState) Load(ctx context.Context) (orchestrationapi.PersistedState, error) {
	state := orchestrationapi.PersistedState{}
	rows, err := s.db.QueryContext(ctx, `
SELECT r.id,r.sequence,r.digest,r.content::text,r.created_at,r.created_by,
       COALESCE(i.key,''),COALESCE(i.fingerprint,'')
FROM cc_config_revisions r
LEFT JOIN cc_idempotency_keys i ON i.scope='revision' AND i.resource_id=r.id
ORDER BY r.sequence,i.key`)
	if err != nil {
		return state, err
	}
	for rows.Next() {
		var revision orchestrationapi.PersistedRevision
		var content string
		if err := rows.Scan(&revision.ID, &revision.Sequence, &revision.Digest, &content, &revision.CreatedAt, &revision.CreatedBy, &revision.IdempotencyKey, &revision.Fingerprint); err != nil {
			rows.Close()
			return state, err
		}
		revision.Content = json.RawMessage(content)
		state.Revisions = append(state.Revisions, revision)
	}
	if err := rows.Close(); err != nil {
		return state, err
	}

	rows, err = s.db.QueryContext(ctx, changeSelect+` ORDER BY c.created_at,c.id`)
	if err != nil {
		return state, err
	}
	for rows.Next() {
		persisted, err := scanPersistedChange(rows)
		if err != nil {
			rows.Close()
			return state, err
		}
		approvals, err := s.loadApprovals(ctx, persisted.Snapshot.ID)
		if err != nil {
			rows.Close()
			return state, err
		}
		persisted.Snapshot.Approvals = approvals
		state.Changes = append(state.Changes, persisted)
	}
	if err := rows.Close(); err != nil {
		return state, err
	}
	return state, nil
}

func (s *OrchestrationState) CreateRevision(ctx context.Context, createdBy, key, fingerprint string, content json.RawMessage, now time.Time) (orchestrationapi.PersistedRevision, error) {
	if createdBy == "" || key == "" || fingerprint == "" || !json.Valid(content) || now.IsZero() {
		return orchestrationapi.PersistedRevision{}, errors.New("complete revision persistence request is required")
	}
	canonical, err := orchestrationconfig.NewRevision("pending", 1, now, content)
	if err != nil {
		return orchestrationapi.PersistedRevision{}, err
	}
	canonicalContent := json.RawMessage(canonical.Content())
	digest := canonical.Digest()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return orchestrationapi.PersistedRevision{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "revision:"+key); err != nil {
		return orchestrationapi.PersistedRevision{}, err
	}
	if existing, storedFingerprint, found, err := revisionByIdempotency(ctx, tx, key); err != nil {
		return orchestrationapi.PersistedRevision{}, err
	} else if found {
		if storedFingerprint != fingerprint {
			return orchestrationapi.PersistedRevision{}, job.ErrIdempotencyConflict
		}
		return existing, tx.Commit()
	}
	if existing, found, err := revisionByDigest(ctx, tx, digest); err != nil {
		return orchestrationapi.PersistedRevision{}, err
	} else if found {
		if _, err := tx.ExecContext(ctx, `INSERT INTO cc_idempotency_keys(scope,key,fingerprint,resource_id,created_at) VALUES ('revision',$1,$2,$3,$4)`, key, fingerprint, existing.ID, now.UTC()); err != nil {
			return orchestrationapi.PersistedRevision{}, err
		}
		existing.IdempotencyKey, existing.Fingerprint = key, fingerprint
		return existing, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `LOCK TABLE cc_config_revisions IN EXCLUSIVE MODE`); err != nil {
		return orchestrationapi.PersistedRevision{}, err
	}
	var sequence uint64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM cc_config_revisions`).Scan(&sequence); err != nil {
		return orchestrationapi.PersistedRevision{}, err
	}
	revision := orchestrationapi.PersistedRevision{ID: "rev-" + randomHex(12), Sequence: sequence, Digest: digest, Content: canonicalContent, CreatedAt: now.UTC(), CreatedBy: createdBy, IdempotencyKey: key, Fingerprint: fingerprint}
	_, err = tx.ExecContext(ctx, `INSERT INTO cc_config_revisions(id,sequence,digest,content,created_at,created_by) VALUES ($1,$2,$3,$4::jsonb,$5,$6)`, revision.ID, int64(revision.Sequence), revision.Digest, string(revision.Content), revision.CreatedAt, revision.CreatedBy)
	if err != nil {
		return orchestrationapi.PersistedRevision{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO cc_idempotency_keys(scope,key,fingerprint,resource_id,created_at) VALUES ('revision',$1,$2,$3,$4)`, key, fingerprint, revision.ID, now.UTC())
	if err != nil {
		return orchestrationapi.PersistedRevision{}, err
	}
	if err := tx.Commit(); err != nil {
		return orchestrationapi.PersistedRevision{}, err
	}
	return revision, nil
}

func revisionByIdempotency(ctx context.Context, tx *sql.Tx, key string) (orchestrationapi.PersistedRevision, string, bool, error) {
	var revision orchestrationapi.PersistedRevision
	var content, fingerprint string
	err := tx.QueryRowContext(ctx, `SELECT r.id,r.sequence,r.digest,r.content::text,r.created_at,r.created_by,i.fingerprint FROM cc_idempotency_keys i JOIN cc_config_revisions r ON r.id=i.resource_id WHERE i.scope='revision' AND i.key=$1`, key).Scan(&revision.ID, &revision.Sequence, &revision.Digest, &content, &revision.CreatedAt, &revision.CreatedBy, &fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return revision, "", false, nil
	}
	if err != nil {
		return revision, "", false, err
	}
	revision.Content, revision.IdempotencyKey, revision.Fingerprint = json.RawMessage(content), key, fingerprint
	return revision, fingerprint, true, nil
}

func revisionByDigest(ctx context.Context, tx *sql.Tx, digest string) (orchestrationapi.PersistedRevision, bool, error) {
	var revision orchestrationapi.PersistedRevision
	var content string
	err := tx.QueryRowContext(ctx, `SELECT id,sequence,digest,content::text,created_at,created_by FROM cc_config_revisions WHERE digest=$1`, digest).Scan(&revision.ID, &revision.Sequence, &revision.Digest, &content, &revision.CreatedAt, &revision.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return revision, false, nil
	}
	revision.Content = json.RawMessage(content)
	return revision, err == nil, err
}

func (s *OrchestrationState) CreateChange(ctx context.Context, requested orchestrationapi.PersistedChange) (orchestrationapi.PersistedChange, bool, error) {
	if requested.Snapshot.ID == "" || requested.IdempotencyKey == "" || requested.Fingerprint == "" || !json.Valid(requested.Input) {
		return orchestrationapi.PersistedChange{}, false, errors.New("complete change persistence request is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return orchestrationapi.PersistedChange{}, false, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "change:"+requested.IdempotencyKey); err != nil {
		return orchestrationapi.PersistedChange{}, false, err
	}
	var existingID, existingFingerprint string
	err = tx.QueryRowContext(ctx, `SELECT resource_id,fingerprint FROM cc_idempotency_keys WHERE scope='change' AND key=$1`, requested.IdempotencyKey).Scan(&existingID, &existingFingerprint)
	if err == nil {
		if existingFingerprint != requested.Fingerprint {
			return orchestrationapi.PersistedChange{}, false, job.ErrIdempotencyConflict
		}
		existing, err := loadChange(ctx, tx, existingID)
		if err != nil {
			return orchestrationapi.PersistedChange{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return orchestrationapi.PersistedChange{}, false, err
		}
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return orchestrationapi.PersistedChange{}, false, err
	}
	decisionID := "decision-" + requested.Snapshot.ID
	d := requested.Snapshot.Decision
	_, err = tx.ExecContext(ctx, `INSERT INTO cc_policy_decisions (id,policy_id,effect,risk,reason,minimum_approvals,approval_permission,distinct_actors,prohibit_requester,evaluated_at) VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10)`, decisionID, d.PolicyID, string(d.Effect), string(d.Risk), d.Reason, d.Requirement.Minimum, d.Requirement.Permission, d.Requirement.DistinctActors, d.Requirement.ProhibitRequester, requested.Snapshot.UpdatedAt)
	if err != nil {
		return orchestrationapi.PersistedChange{}, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO cc_changes (id,action_name,requester,revision_id,decision_id,risk,input,idempotency_key,input_fingerprint,job_id,state,version,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,NULLIF($10,''),$11,$12,$13,$13)`, requested.Snapshot.ID, requested.Snapshot.Action, requested.Snapshot.Requester, requested.Snapshot.RevisionID, decisionID, string(requested.Snapshot.Risk), string(requested.Input), requested.IdempotencyKey, requested.Fingerprint, requested.JobID, string(requested.Snapshot.State), int64(requested.Snapshot.Version), requested.Snapshot.UpdatedAt)
	if err != nil {
		return orchestrationapi.PersistedChange{}, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO cc_idempotency_keys(scope,key,fingerprint,resource_id,created_at) VALUES ('change',$1,$2,$3,$4)`, requested.IdempotencyKey, requested.Fingerprint, requested.Snapshot.ID, requested.Snapshot.UpdatedAt)
	if err != nil {
		return orchestrationapi.PersistedChange{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return orchestrationapi.PersistedChange{}, false, err
	}
	return requested, true, nil
}

func (s *OrchestrationState) UpdateChange(ctx context.Context, updated orchestrationapi.PersistedChange) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE cc_changes SET state=$2,version=$3,updated_at=$4,job_id=NULLIF($5,'') WHERE id=$1 AND version <= $3`, updated.Snapshot.ID, string(updated.Snapshot.State), int64(updated.Snapshot.Version), updated.Snapshot.UpdatedAt.UTC(), updated.JobID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("change persistence version conflict")
	}
	for _, approval := range updated.Snapshot.Approvals {
		permissions, err := json.Marshal(approval.Permissions)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO cc_change_approvals(change_id,actor,permissions,approved_at) VALUES ($1,$2,ARRAY(SELECT jsonb_array_elements_text($3::jsonb)),$4) ON CONFLICT (change_id,actor) DO NOTHING`, updated.Snapshot.ID, approval.Actor, string(permissions), approval.ApprovedAt.UTC())
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

const changeSelect = `
SELECT c.id,c.action_name,c.requester,c.revision_id,c.risk,c.state,c.version,c.updated_at,
       c.input::text,c.idempotency_key,c.input_fingerprint,COALESCE(c.job_id,''),
       d.effect,d.risk,d.reason,d.minimum_approvals,COALESCE(d.approval_permission,''),
       d.distinct_actors,d.prohibit_requester,d.policy_id
FROM cc_changes c JOIN cc_policy_decisions d ON d.id=c.decision_id`

func loadChange(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (orchestrationapi.PersistedChange, error) {
	persisted, err := scanPersistedChange(query.QueryRowContext(ctx, changeSelect+` WHERE c.id=$1`, id))
	if err != nil {
		return persisted, err
	}
	rows, err := queryApprovals(ctx, query, id)
	if err != nil {
		return persisted, err
	}
	persisted.Snapshot.Approvals = rows
	return persisted, nil
}

func scanPersistedChange(row scanner) (orchestrationapi.PersistedChange, error) {
	var persisted orchestrationapi.PersistedChange
	var input, risk, state, effect, decisionRisk, permissions string
	err := row.Scan(&persisted.Snapshot.ID, &persisted.Snapshot.Action, &persisted.Snapshot.Requester, &persisted.Snapshot.RevisionID, &risk, &state, &persisted.Snapshot.Version, &persisted.Snapshot.UpdatedAt, &input, &persisted.IdempotencyKey, &persisted.Fingerprint, &persisted.JobID, &effect, &decisionRisk, &persisted.Snapshot.Decision.Reason, &persisted.Snapshot.Decision.Requirement.Minimum, &permissions, &persisted.Snapshot.Decision.Requirement.DistinctActors, &persisted.Snapshot.Decision.Requirement.ProhibitRequester, &persisted.Snapshot.Decision.PolicyID)
	if err != nil {
		return persisted, err
	}
	persisted.Input = json.RawMessage(input)
	persisted.Snapshot.Risk = policy.Risk(risk)
	persisted.Snapshot.State = change.State(state)
	persisted.Snapshot.Decision.Effect = policy.Effect(effect)
	persisted.Snapshot.Decision.Risk = policy.Risk(decisionRisk)
	persisted.Snapshot.Decision.Requirement.Permission = permissions
	return persisted, nil
}

func (s *OrchestrationState) loadApprovals(ctx context.Context, changeID string) ([]policy.Approval, error) {
	return queryApprovals(ctx, s.db, changeID)
}

type approvalQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func queryApprovals(ctx context.Context, query any, changeID string) ([]policy.Approval, error) {
	querier, ok := query.(approvalQuerier)
	if !ok {
		return nil, errors.New("approval query is not supported")
	}
	rows, err := querier.QueryContext(ctx, `SELECT actor,to_json(permissions)::text,approved_at FROM cc_change_approvals WHERE change_id=$1 ORDER BY approved_at,actor`, changeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	approvals := make([]policy.Approval, 0)
	for rows.Next() {
		var approval policy.Approval
		var permissions string
		if err := rows.Scan(&approval.Actor, &permissions, &approval.ApprovedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(permissions), &approval.Permissions); err != nil {
			return nil, err
		}
		approvals = append(approvals, approval)
	}
	return approvals, rows.Err()
}
