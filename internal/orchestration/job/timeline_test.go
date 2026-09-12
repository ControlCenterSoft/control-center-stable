package job_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"control-center/internal/orchestration/job"
)

func TestMemoryTimelineRepositoryIsAppendOnlyAndIdempotent(t *testing.T) {
	repository := job.NewMemoryTimelineRepository()
	ctx := context.Background()
	now := time.Unix(100, 0)
	created := job.TimelineEntry{JobID: "job-1", Event: job.TimelineCreated, Status: job.StatusQueued, Attempt: 0, JobVersion: 1, OccurredAt: now}
	started := job.TimelineEntry{JobID: "job-1", Event: job.TimelineAttemptStarted, Status: job.StatusRunning, Attempt: 1, JobVersion: 2, OccurredAt: now.Add(time.Second)}

	if ok, err := repository.Append(ctx, created); err != nil || !ok {
		t.Fatalf("append created = %v, %v", ok, err)
	}
	if ok, err := repository.Append(ctx, created); err != nil || ok {
		t.Fatalf("idempotent append = %v, %v", ok, err)
	}
	if ok, err := repository.Append(ctx, started); err != nil || !ok {
		t.Fatalf("append started = %v, %v", ok, err)
	}

	conflict := started
	conflict.Status = job.StatusFailed
	conflict.Event = job.TimelineFailed
	if _, err := repository.Append(ctx, conflict); !errors.Is(err, job.ErrTimelineConflict) {
		t.Fatalf("conflict error = %v", err)
	}

	entries, err := repository.List(ctx, "job-1")
	if err != nil || len(entries) != 2 {
		t.Fatalf("list = %#v, %v", entries, err)
	}
	if entries[0].Event != job.TimelineCreated || entries[1].Event != job.TimelineAttemptStarted {
		t.Fatalf("unexpected timeline order: %#v", entries)
	}
}

func TestValidateTimelineEntryFailsClosed(t *testing.T) {
	now := time.Unix(100, 0)
	cases := []job.TimelineEntry{
		{JobID: " job-1", Event: job.TimelineCreated, Status: job.StatusQueued, JobVersion: 1, OccurredAt: now},
		{JobID: "job-1", Event: job.TimelineSucceeded, Status: job.StatusRunning, JobVersion: 2, OccurredAt: now},
		{JobID: "job-1", Event: "unknown", Status: job.StatusQueued, JobVersion: 1, OccurredAt: now},
		{JobID: "job-1", Event: job.TimelineCreated, Status: job.StatusQueued, Attempt: 1, JobVersion: 1, OccurredAt: now},
	}
	for index, entry := range cases {
		if err := job.ValidateTimelineEntry(entry); !errors.Is(err, job.ErrInvalidTimelineEntry) {
			t.Fatalf("case %d error = %v", index, err)
		}
	}
}
