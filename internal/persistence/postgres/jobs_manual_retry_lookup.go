package postgres

import (
	"context"
	"database/sql"
	"errors"

	"control-center/internal/orchestration/job"
)

func (r *JobRepository) GetManualRetryLineageByRetryJob(ctx context.Context, retryJobID string) (job.ManualRetryLineage, error) {
	lineage, _, err := scanManualRetryLineage(r.db.QueryRowContext(ctx, `SELECT `+manualRetryLineageColumns+` FROM cc_job_manual_retry_lineage WHERE retry_job_id=$1`, retryJobID))
	if errors.Is(err, sql.ErrNoRows) {
		return job.ManualRetryLineage{}, job.ErrManualRetryLineageNotFound
	}
	return lineage, err
}

var _ job.ManualRetryLineageReader = (*JobRepository)(nil)
