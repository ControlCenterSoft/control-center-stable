package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"control-center/internal/orchestration/job"
)

type JobTimelineRepository struct{ db *sql.DB }

func NewJobTimelineRepository(db *sql.DB) (*JobTimelineRepository, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	return &JobTimelineRepository{db: db}, nil
}

func (r *JobTimelineRepository) Append(ctx context.Context, entry job.TimelineEntry) (bool, error) {
	if err := job.ValidateTimelineEntry(entry); err != nil {
		return false, err
	}
	entry.OccurredAt = entry.OccurredAt.UTC()
	result, err := r.db.ExecContext(ctx, `
INSERT INTO cc_job_timeline (job_id, job_version, event, status, attempt, occurred_at)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (job_id, job_version) DO NOTHING`, entry.JobID, entry.JobVersion, string(entry.Event), string(entry.Status), entry.Attempt, entry.OccurredAt)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if rows == 1 {
		return true, nil
	}
	existing, err := r.get(ctx, entry.JobID, entry.JobVersion)
	if err != nil {
		return false, err
	}
	if sameTimelineEntry(existing, entry) {
		return false, nil
	}
	return false, job.ErrTimelineConflict
}

func (r *JobTimelineRepository) List(ctx context.Context, jobID string) ([]job.TimelineEntry, error) {
	if strings.TrimSpace(jobID) == "" || jobID != strings.TrimSpace(jobID) {
		return nil, job.ErrInvalidTimelineEntry
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT job_id,event,status,attempt,job_version,occurred_at
FROM cc_job_timeline
WHERE job_id=$1
ORDER BY job_version,occurred_at`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]job.TimelineEntry, 0)
	for rows.Next() {
		entry, err := scanTimelineEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (r *JobTimelineRepository) get(ctx context.Context, jobID string, version uint64) (job.TimelineEntry, error) {
	return scanTimelineEntry(r.db.QueryRowContext(ctx, `
SELECT job_id,event,status,attempt,job_version,occurred_at
FROM cc_job_timeline
WHERE job_id=$1 AND job_version=$2`, jobID, version))
}

type timelineScanner interface{ Scan(...any) error }

func scanTimelineEntry(row timelineScanner) (job.TimelineEntry, error) {
	var entry job.TimelineEntry
	var event, status string
	if err := row.Scan(&entry.JobID, &event, &status, &entry.Attempt, &entry.JobVersion, &entry.OccurredAt); err != nil {
		return job.TimelineEntry{}, err
	}
	entry.Event = job.TimelineEvent(event)
	entry.Status = job.Status(status)
	entry.OccurredAt = entry.OccurredAt.UTC()
	if err := job.ValidateTimelineEntry(entry); err != nil {
		return job.TimelineEntry{}, err
	}
	return entry, nil
}

func sameTimelineEntry(left, right job.TimelineEntry) bool {
	return left.JobID == right.JobID &&
		left.Event == right.Event &&
		left.Status == right.Status &&
		left.Attempt == right.Attempt &&
		left.JobVersion == right.JobVersion &&
		left.OccurredAt.Equal(right.OccurredAt)
}

var _ job.TimelineRepository = (*JobTimelineRepository)(nil)
