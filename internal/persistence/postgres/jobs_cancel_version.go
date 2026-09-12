package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"control-center/internal/orchestration/job"
)

// RequestCancelIfVersion provides compare-and-swap cancellation for operator
// flows. A stale version fails closed and leaves the durable Job unchanged.
func (r *JobRepository) RequestCancelIfVersion(ctx context.Context, id string, expectedVersion uint64, now time.Time) (job.Job, error) {
	if expectedVersion == 0 {
		return job.Job{}, errors.New("expected job version is required")
	}
	result, err := scanJob(r.db.QueryRowContext(ctx, `UPDATE cc_jobs SET status=CASE WHEN status IN ('cancelled','succeeded','failed') THEN status WHEN status='running' THEN 'cancel_requested' ELSE 'cancelled' END,lease_token=CASE WHEN status='running' THEN lease_token ELSE NULL END,lease_worker_id=CASE WHEN status='running' THEN lease_worker_id ELSE NULL END,lease_expires_at=CASE WHEN status='running' THEN lease_expires_at ELSE NULL END,updated_at=CASE WHEN status IN ('cancelled','succeeded','failed') THEN updated_at ELSE $2 END,version=CASE WHEN status IN ('cancelled','succeeded','failed') THEN version ELSE version+1 END WHERE id=$1 AND version=$3 RETURNING `+jobColumns, id, now.UTC(), expectedVersion))
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if existsErr := r.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cc_jobs WHERE id=$1)`, id).Scan(&exists); existsErr != nil {
			return job.Job{}, existsErr
		}
		if !exists {
			return job.Job{}, job.ErrNotFound
		}
		return job.Job{}, job.ErrVersionConflict
	}
	return result, err
}

var _ job.VersionedCancellationRepository = (*JobRepository)(nil)
