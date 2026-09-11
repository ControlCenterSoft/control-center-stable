package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"control-center/internal/orchestration/events"
	"control-center/internal/orchestration/job"
)

type JobRepository struct{ db *sql.DB }

func NewJobRepository(db *sql.DB) (*JobRepository, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	return &JobRepository{db: db}, nil
}

func (r *JobRepository) Create(ctx context.Context, request job.CreateRequest) (job.Job, bool, error) {
	if request.ID == "" || request.ChangeID == "" || request.ActionName == "" || request.IdempotencyKey == "" || len(request.Input) == 0 || !json.Valid(request.Input) || request.MaxAttempts < 1 || request.Now.IsZero() {
		return job.Job{}, false, errors.New("complete valid job creation request is required")
	}
	fingerprint := jobFingerprint(request)
	created, err := scanJob(r.db.QueryRowContext(ctx, jobInsertSQL+` ON CONFLICT (idempotency_key) DO NOTHING RETURNING `+jobColumns, request.ID, request.ChangeID, request.ActionName, string(request.Input), request.IdempotencyKey, fingerprint, request.MaxAttempts, request.Now.UTC()))
	if err == nil {
		return created, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		if isUniqueViolation(err) {
			return job.Job{}, false, fmt.Errorf("job id already exists: %s", request.ID)
		}
		return job.Job{}, false, err
	}
	existing, storedFingerprint, err := r.byIdempotency(ctx, request.IdempotencyKey)
	if err != nil {
		return job.Job{}, false, err
	}
	if storedFingerprint != fingerprint {
		return job.Job{}, false, job.ErrIdempotencyConflict
	}
	return existing, false, nil
}

const jobInsertSQL = `
INSERT INTO cc_jobs
  (id,change_id,action_name,input,idempotency_key,input_fingerprint,status,attempt,max_attempts,version,created_at,updated_at)
VALUES ($1,$2,$3,$4::jsonb,$5,$6,'queued',0,$7,1,$8,$8) `

const jobColumns = `id,change_id,action_name,input::text,idempotency_key,status,attempt,max_attempts,
next_attempt_at,lease_token,lease_worker_id,lease_expires_at,output::text,last_error,created_at,updated_at,version`

func (r *JobRepository) byIdempotency(ctx context.Context, key string) (job.Job, string, error) {
	var fingerprint string
	result, err := scanJobWithExtra(r.db.QueryRowContext(ctx, `SELECT `+jobColumns+`,input_fingerprint FROM cc_jobs WHERE idempotency_key=$1`, key), &fingerprint)
	return result, fingerprint, err
}

func (r *JobRepository) Get(ctx context.Context, id string) (job.Job, error) {
	result, err := scanJob(r.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM cc_jobs WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return job.Job{}, job.ErrNotFound
	}
	return result, err
}

func (r *JobRepository) List(ctx context.Context, filter job.Filter) ([]job.Job, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM cc_jobs WHERE ($1='' OR change_id=$1) AND ($2='' OR status=$2) ORDER BY created_at,id`, filter.ChangeID, string(filter.Status))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]job.Job, 0)
	for rows.Next() {
		item, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *JobRepository) Claim(ctx context.Context, workerID string, now time.Time, ttl time.Duration) (job.Job, bool, error) {
	if workerID == "" || now.IsZero() || ttl <= 0 {
		return job.Job{}, false, errors.New("worker id, current time, and positive lease ttl are required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return job.Job{}, false, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE cc_jobs SET status='cancelled',lease_token=NULL,lease_worker_id=NULL,lease_expires_at=NULL,updated_at=$1,version=version+1 WHERE status='cancel_requested' AND lease_expires_at <= $1`, now.UTC())
	if err != nil {
		return job.Job{}, false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE cc_jobs SET status='failed',last_error='maximum attempts exhausted',lease_token=NULL,lease_worker_id=NULL,lease_expires_at=NULL,updated_at=$1,version=version+1 WHERE attempt>=max_attempts AND (status='queued' OR status='retry_wait' OR (status='running' AND lease_expires_at <= $1))`, now.UTC())
	if err != nil {
		return job.Job{}, false, err
	}
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM cc_jobs WHERE attempt < max_attempts AND (status='queued' OR (status='retry_wait' AND (next_attempt_at IS NULL OR next_attempt_at <= $1)) OR (status='running' AND lease_expires_at <= $1)) ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1`, now.UTC()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return job.Job{}, false, err
		}
		return job.Job{}, false, nil
	}
	if err != nil {
		return job.Job{}, false, err
	}
	token := randomHex(32)
	claimed, err := scanJob(tx.QueryRowContext(ctx, `UPDATE cc_jobs SET status='running',attempt=attempt+1,next_attempt_at=NULL,last_error=NULL,lease_token=$2,lease_worker_id=$3,lease_expires_at=$4,updated_at=$1,version=version+1 WHERE id=$5 RETURNING `+jobColumns, now.UTC(), token, workerID, now.UTC().Add(ttl), id))
	if err != nil {
		return job.Job{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return job.Job{}, false, err
	}
	return claimed, true, nil
}

func (r *JobRepository) RenewLease(ctx context.Context, id, token string, now time.Time, ttl time.Duration) (job.Job, error) {
	if ttl <= 0 {
		return job.Job{}, errors.New("lease ttl must be positive")
	}
	result, err := scanJob(r.db.QueryRowContext(ctx, `UPDATE cc_jobs SET lease_expires_at=$4,updated_at=$3,version=version+1 WHERE id=$1 AND lease_token=$2 AND lease_expires_at>$3 AND status IN ('running','cancel_requested') RETURNING `+jobColumns, id, token, now.UTC(), now.UTC().Add(ttl)))
	if errors.Is(err, sql.ErrNoRows) {
		return job.Job{}, job.ErrLeaseLost
	}
	return result, err
}

func (r *JobRepository) Succeed(ctx context.Context, id, token string, output events.Output, now time.Time) (job.Job, error) {
	payload, err := json.Marshal(output)
	if err != nil {
		return job.Job{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return job.Job{}, err
	}
	defer tx.Rollback()
	result, err := scanJob(tx.QueryRowContext(ctx, `UPDATE cc_jobs SET status=CASE WHEN status='cancel_requested' THEN 'cancelled' ELSE 'succeeded' END,output=CASE WHEN status='cancel_requested' THEN output ELSE $3::jsonb END,lease_token=NULL,lease_worker_id=NULL,lease_expires_at=NULL,updated_at=$4,version=version+1 WHERE id=$1 AND lease_token=$2 AND lease_expires_at>$4 AND status IN ('running','cancel_requested') RETURNING `+jobColumns, id, token, string(payload), now.UTC()))
	if errors.Is(err, sql.ErrNoRows) {
		return job.Job{}, job.ErrLeaseLost
	}
	if err != nil {
		return job.Job{}, err
	}
	if result.Status == job.StatusSucceeded {
		if err := persistOutputs(ctx, tx, id, output); err != nil {
			return job.Job{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return job.Job{}, err
	}
	return result, nil
}

func (r *JobRepository) Fail(ctx context.Context, id, token, message string, output events.Output, retry job.RetryPolicy, now time.Time) (job.Job, error) {
	if message == "" {
		return job.Job{}, errors.New("failure message is required")
	}
	payload, err := json.Marshal(output)
	if err != nil {
		return job.Job{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return job.Job{}, err
	}
	defer tx.Rollback()
	current, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM cc_jobs WHERE id=$1 AND lease_token=$2 AND lease_expires_at>$3 AND status IN ('running','cancel_requested') FOR UPDATE`, id, token, now.UTC()))
	if errors.Is(err, sql.ErrNoRows) {
		return job.Job{}, job.ErrLeaseLost
	}
	if err != nil {
		return job.Job{}, err
	}
	status := job.StatusFailed
	var next any
	if current.Status == job.StatusCancelRequested {
		status = job.StatusCancelled
	} else if current.Attempt < current.MaxAttempts {
		status = job.StatusRetryWait
		next = now.UTC().Add(retryDelay(current.Attempt, retry))
	}
	result, err := scanJob(tx.QueryRowContext(ctx, `UPDATE cc_jobs SET status=$3,next_attempt_at=$4,last_error=$5,output=$6::jsonb,lease_token=NULL,lease_worker_id=NULL,lease_expires_at=NULL,updated_at=$7,version=version+1 WHERE id=$1 AND lease_token=$2 RETURNING `+jobColumns, id, token, string(status), next, message, string(payload), now.UTC()))
	if err != nil {
		return job.Job{}, err
	}
	if err := persistOutputs(ctx, tx, id, output); err != nil {
		return job.Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return job.Job{}, err
	}
	return result, nil
}

func (r *JobRepository) RequestCancel(ctx context.Context, id string, now time.Time) (job.Job, error) {
	result, err := scanJob(r.db.QueryRowContext(ctx, `UPDATE cc_jobs SET status=CASE WHEN status IN ('cancelled','succeeded','failed') THEN status WHEN status='running' THEN 'cancel_requested' ELSE 'cancelled' END,lease_token=CASE WHEN status='running' THEN lease_token ELSE NULL END,lease_worker_id=CASE WHEN status='running' THEN lease_worker_id ELSE NULL END,lease_expires_at=CASE WHEN status='running' THEN lease_expires_at ELSE NULL END,updated_at=CASE WHEN status IN ('cancelled','succeeded','failed') THEN updated_at ELSE $2 END,version=CASE WHEN status IN ('cancelled','succeeded','failed') THEN version ELSE version+1 END WHERE id=$1 RETURNING `+jobColumns, id, now.UTC()))
	if errors.Is(err, sql.ErrNoRows) {
		return job.Job{}, job.ErrNotFound
	}
	return result, err
}

type scanner interface{ Scan(...any) error }

func scanJob(row scanner) (job.Job, error) { return scanJobWithExtra(row, nil) }
func scanJobWithExtra(row scanner, extra *string) (job.Job, error) {
	var result job.Job
	var input string
	var status string
	var nextAttempt, leaseExpires sql.NullTime
	var leaseToken, leaseWorker, outputJSON, lastError sql.NullString
	targets := []any{&result.ID, &result.ChangeID, &result.ActionName, &input, &result.IdempotencyKey, &status, &result.Attempt, &result.MaxAttempts, &nextAttempt, &leaseToken, &leaseWorker, &leaseExpires, &outputJSON, &lastError, &result.CreatedAt, &result.UpdatedAt, &result.Version}
	if extra != nil {
		targets = append(targets, extra)
	}
	if err := row.Scan(targets...); err != nil {
		return job.Job{}, err
	}
	result.Input = json.RawMessage(input)
	result.Status = job.Status(status)
	if nextAttempt.Valid {
		result.NextAttemptAt = nextAttempt.Time.UTC()
	}
	if leaseToken.Valid && leaseWorker.Valid && leaseExpires.Valid {
		result.Lease = &job.Lease{Token: leaseToken.String, WorkerID: leaseWorker.String, ExpiresAt: leaseExpires.Time.UTC()}
	}
	if outputJSON.Valid {
		var output events.Output
		if err := json.Unmarshal([]byte(outputJSON.String), &output); err != nil {
			return job.Job{}, err
		}
		result.Output = &output
	}
	result.LastError = lastError.String
	return result, nil
}
func persistOutputs(ctx context.Context, tx *sql.Tx, jobID string, output events.Output) error {
	for _, actual := range output.ActualStates {
		_, err := tx.ExecContext(ctx, `INSERT INTO cc_actual_states (job_id,resource_id,kind,state,observed_at,revision_id,details) VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,'')::jsonb) ON CONFLICT (job_id,resource_id) DO UPDATE SET kind=EXCLUDED.kind,state=EXCLUDED.state,observed_at=EXCLUDED.observed_at,revision_id=EXCLUDED.revision_id,details=EXCLUDED.details`, jobID, actual.ResourceID, actual.Kind, string(actual.State), actual.ObservedAt.UTC(), actual.Revision, string(actual.Details))
		if err != nil {
			return err
		}
	}
	for _, health := range output.Health {
		_, err := tx.ExecContext(ctx, `INSERT INTO cc_health_observations (job_id,resource_id,status,checked_at,message) VALUES ($1,$2,$3,$4,NULLIF($5,'')) ON CONFLICT (job_id,resource_id) DO UPDATE SET status=EXCLUDED.status,checked_at=EXCLUDED.checked_at,message=EXCLUDED.message`, jobID, health.ResourceID, string(health.Status), health.CheckedAt.UTC(), health.Message)
		if err != nil {
			return err
		}
	}
	return nil
}
func jobFingerprint(request job.CreateRequest) string {
	h := sha256.New()
	h.Write([]byte(request.ChangeID))
	h.Write([]byte{0})
	h.Write([]byte(request.ActionName))
	h.Write([]byte{0})
	h.Write(request.Input)
	return hex.EncodeToString(h.Sum(nil))
}
func retryDelay(attempt int, retry job.RetryPolicy) time.Duration {
	base := retry.BaseDelay
	if base <= 0 {
		base = time.Second
	}
	delay := base
	for i := 1; i < attempt; i++ {
		delay *= 2
	}
	if retry.MaxDelay > 0 && delay > retry.MaxDelay {
		return retry.MaxDelay
	}
	return delay
}
func randomHex(size int) string {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		panic("operating system random source unavailable")
	}
	return hex.EncodeToString(value)
}
