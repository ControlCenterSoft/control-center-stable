package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"control-center/internal/orchestration/job"
)

const manualRetryLineageColumns = `root_job_id,source_job_id,source_job_version,retry_job_id,retry_idempotency_key,
reviewed_admission_id,revalidation_admission_id,revision_id,revision_digest,policy_id,policy_digest,
retry_history_digest,approval_evidence_digest,requested_at,request_fingerprint`

// CreateManualRetry atomically preserves one failed source Job and creates one
// fresh queued Job plus immutable retry-lineage evidence. The source row is
// locked before replay/source-lineage detection so concurrent requests for the
// same source version are serialized and cannot create two child Jobs.
func (r *JobRepository) CreateManualRetry(ctx context.Context, request job.ManualRetryRequest) (job.Job, job.ManualRetryLineage, bool, error) {
	if err := ctx.Err(); err != nil {
		return job.Job{}, job.ManualRetryLineage{}, false, err
	}
	requestFingerprint, err := job.ManualRetryRequestFingerprint(request)
	if err != nil {
		return job.Job{}, job.ManualRetryLineage{}, false, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return job.Job{}, job.ManualRetryLineage{}, false, err
	}
	defer tx.Rollback()

	source, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM cc_jobs WHERE id=$1 FOR UPDATE`, request.SourceJobID))
	if errors.Is(err, sql.ErrNoRows) {
		return job.Job{}, job.ManualRetryLineage{}, false, job.ErrNotFound
	}
	if err != nil {
		return job.Job{}, job.ManualRetryLineage{}, false, err
	}
	if source.Version != request.ExpectedSourceVersion {
		return job.Job{}, job.ManualRetryLineage{}, false, job.ErrVersionConflict
	}

	existing, storedFingerprint, err := scanManualRetryLineage(tx.QueryRowContext(ctx, `SELECT `+manualRetryLineageColumns+` FROM cc_job_manual_retry_lineage WHERE reviewed_admission_id=$1`, request.ReviewedAdmissionID))
	if err == nil {
		if storedFingerprint != requestFingerprint {
			return job.Job{}, job.ManualRetryLineage{}, false, job.ErrManualRetryAdmissionConflict
		}
		retry, getErr := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM cc_jobs WHERE id=$1`, existing.RetryJobID))
		if getErr != nil {
			return job.Job{}, job.ManualRetryLineage{}, false, getErr
		}
		if err := tx.Commit(); err != nil {
			return job.Job{}, job.ManualRetryLineage{}, false, err
		}
		return retry, existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return job.Job{}, job.ManualRetryLineage{}, false, err
	}

	if source.Status != job.StatusFailed || source.Attempt < source.MaxAttempts || source.Lease != nil {
		return job.Job{}, job.ManualRetryLineage{}, false, job.ErrManualRetrySourceNotFailed
	}
	var priorSourceAdmission string
	err = tx.QueryRowContext(ctx, `SELECT reviewed_admission_id FROM cc_job_manual_retry_lineage WHERE source_job_id=$1 AND source_job_version=$2`, source.ID, source.Version).Scan(&priorSourceAdmission)
	if err == nil {
		return job.Job{}, job.ManualRetryLineage{}, false, job.ErrManualRetrySourceAlreadyRetried
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return job.Job{}, job.ManualRetryLineage{}, false, err
	}

	rootJobID := source.ID
	var parentRoot string
	err = tx.QueryRowContext(ctx, `SELECT root_job_id FROM cc_job_manual_retry_lineage WHERE retry_job_id=$1`, source.ID).Scan(&parentRoot)
	if err == nil {
		rootJobID = parentRoot
	} else if !errors.Is(err, sql.ErrNoRows) {
		return job.Job{}, job.ManualRetryLineage{}, false, err
	}

	now := request.RequestedAt.UTC()
	retryRequest := job.CreateRequest{
		ID:             request.RetryJobID,
		ChangeID:       source.ChangeID,
		ActionName:     source.ActionName,
		Input:          append([]byte(nil), source.Input...),
		IdempotencyKey: request.RetryIdempotencyKey,
		MaxAttempts:    source.MaxAttempts,
		Now:            now,
	}
	retry, err := scanJob(tx.QueryRowContext(ctx, jobInsertSQL+` ON CONFLICT DO NOTHING RETURNING `+jobColumns,
		retryRequest.ID,
		retryRequest.ChangeID,
		retryRequest.ActionName,
		string(retryRequest.Input),
		retryRequest.IdempotencyKey,
		jobFingerprint(retryRequest),
		retryRequest.MaxAttempts,
		now,
	))
	if errors.Is(err, sql.ErrNoRows) {
		var existingJobID string
		var existingIdempotencyJobID string
		idErr := tx.QueryRowContext(ctx, `SELECT id FROM cc_jobs WHERE id=$1`, retryRequest.ID).Scan(&existingJobID)
		keyErr := tx.QueryRowContext(ctx, `SELECT id FROM cc_jobs WHERE idempotency_key=$1`, retryRequest.IdempotencyKey).Scan(&existingIdempotencyJobID)
		if keyErr == nil {
			return job.Job{}, job.ManualRetryLineage{}, false, job.ErrIdempotencyConflict
		}
		if idErr == nil {
			return job.Job{}, job.ManualRetryLineage{}, false, fmt.Errorf("retry job id already exists: %s", existingJobID)
		}
		if !errors.Is(idErr, sql.ErrNoRows) {
			return job.Job{}, job.ManualRetryLineage{}, false, idErr
		}
		if !errors.Is(keyErr, sql.ErrNoRows) {
			return job.Job{}, job.ManualRetryLineage{}, false, keyErr
		}
		return job.Job{}, job.ManualRetryLineage{}, false, errors.New("manual retry job insert was rejected without a visible conflicting row")
	}
	if err != nil {
		return job.Job{}, job.ManualRetryLineage{}, false, err
	}

	var approvalEvidence any
	if request.ApprovalEvidenceDigest != "" {
		approvalEvidence = request.ApprovalEvidenceDigest
	}
	lineage, _, err := scanManualRetryLineage(tx.QueryRowContext(ctx, `
INSERT INTO cc_job_manual_retry_lineage
  (reviewed_admission_id,request_fingerprint,revalidation_admission_id,root_job_id,source_job_id,source_job_version,
   retry_job_id,retry_idempotency_key,revision_id,revision_digest,policy_id,policy_digest,retry_history_digest,
   approval_evidence_digest,requested_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
RETURNING `+manualRetryLineageColumns,
		request.ReviewedAdmissionID,
		requestFingerprint,
		request.RevalidationAdmissionID,
		rootJobID,
		source.ID,
		source.Version,
		retry.ID,
		retry.IdempotencyKey,
		request.RevisionID,
		request.RevisionDigest,
		request.PolicyID,
		request.PolicyDigest,
		request.RetryHistoryDigest,
		approvalEvidence,
		now,
	))
	if err != nil {
		return job.Job{}, job.ManualRetryLineage{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return job.Job{}, job.ManualRetryLineage{}, false, err
	}
	return retry, lineage, true, nil
}

func (r *JobRepository) GetManualRetryLineageByAdmission(ctx context.Context, admissionID string) (job.ManualRetryLineage, error) {
	lineage, _, err := scanManualRetryLineage(r.db.QueryRowContext(ctx, `SELECT `+manualRetryLineageColumns+` FROM cc_job_manual_retry_lineage WHERE reviewed_admission_id=$1`, admissionID))
	if errors.Is(err, sql.ErrNoRows) {
		return job.ManualRetryLineage{}, job.ErrManualRetryLineageNotFound
	}
	return lineage, err
}

func (r *JobRepository) ListManualRetryLineage(ctx context.Context, rootJobID string) ([]job.ManualRetryLineage, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+manualRetryLineageColumns+` FROM cc_job_manual_retry_lineage WHERE root_job_id=$1 ORDER BY requested_at,retry_job_id`, rootJobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]job.ManualRetryLineage, 0)
	for rows.Next() {
		lineage, _, scanErr := scanManualRetryLineage(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, lineage)
	}
	return result, rows.Err()
}

func scanManualRetryLineage(row scanner) (job.ManualRetryLineage, string, error) {
	var result job.ManualRetryLineage
	var approval sql.NullString
	var requestFingerprint string
	err := row.Scan(
		&result.RootJobID,
		&result.SourceJobID,
		&result.SourceJobVersion,
		&result.RetryJobID,
		&result.RetryIdempotencyKey,
		&result.ReviewedAdmissionID,
		&result.RevalidationAdmissionID,
		&result.RevisionID,
		&result.RevisionDigest,
		&result.PolicyID,
		&result.PolicyDigest,
		&result.RetryHistoryDigest,
		&approval,
		&result.RequestedAt,
		&requestFingerprint,
	)
	if err != nil {
		return job.ManualRetryLineage{}, "", err
	}
	if approval.Valid {
		result.ApprovalEvidenceDigest = approval.String
	}
	result.RequestedAt = result.RequestedAt.UTC()
	return result, requestFingerprint, nil
}

var _ job.ManualRetryRepository = (*JobRepository)(nil)
