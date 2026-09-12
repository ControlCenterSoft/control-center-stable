package job_test

import (
	"errors"
	"testing"
	"time"

	"control-center/internal/orchestration/job"
)

func TestTimelineEntryFromTransitionProjectsLifecycleWithoutSecrets(t *testing.T) {
	now := time.Unix(100, 0)
	queued := job.Job{ID: "job-1", Status: job.StatusQueued, Attempt: 0, MaxAttempts: 3, CreatedAt: now, UpdatedAt: now, Version: 1}
	created, ok, err := job.TimelineEntryFromTransition(nil, queued)
	if err != nil || !ok || created.Event != job.TimelineCreated {
		t.Fatalf("created = %#v, %v, %v", created, ok, err)
	}

	running := queued
	running.Status = job.StatusRunning
	running.Attempt = 1
	running.Version = 2
	running.UpdatedAt = now.Add(time.Second)
	started, ok, err := job.TimelineEntryFromTransition(&queued, running)
	if err != nil || !ok || started.Event != job.TimelineAttemptStarted {
		t.Fatalf("started = %#v, %v, %v", started, ok, err)
	}

	retry := running
	retry.Status = job.StatusRetryWait
	retry.Version = 3
	retry.UpdatedAt = now.Add(2 * time.Second)
	scheduled, ok, err := job.TimelineEntryFromTransition(&running, retry)
	if err != nil || !ok || scheduled.Event != job.TimelineRetryScheduled {
		t.Fatalf("retry = %#v, %v, %v", scheduled, ok, err)
	}

	runningAgain := retry
	runningAgain.Status = job.StatusRunning
	runningAgain.Attempt = 2
	runningAgain.Version = 4
	runningAgain.UpdatedAt = now.Add(3 * time.Second)
	startedAgain, ok, err := job.TimelineEntryFromTransition(&retry, runningAgain)
	if err != nil || !ok || startedAgain.Event != job.TimelineAttemptStarted {
		t.Fatalf("started again = %#v, %v, %v", startedAgain, ok, err)
	}

	succeeded := runningAgain
	succeeded.Status = job.StatusSucceeded
	succeeded.Version = 5
	succeeded.UpdatedAt = now.Add(4 * time.Second)
	finished, ok, err := job.TimelineEntryFromTransition(&runningAgain, succeeded)
	if err != nil || !ok || finished.Event != job.TimelineSucceeded {
		t.Fatalf("finished = %#v, %v, %v", finished, ok, err)
	}
}

func TestTimelineEntryFromTransitionIgnoresLeaseRenewal(t *testing.T) {
	now := time.Unix(100, 0)
	before := job.Job{ID: "job-1", Status: job.StatusRunning, Attempt: 1, MaxAttempts: 3, UpdatedAt: now, Version: 2}
	after := before
	after.Version = 3
	after.UpdatedAt = now.Add(time.Second)
	entry, ok, err := job.TimelineEntryFromTransition(&before, after)
	if err != nil || ok || entry != (job.TimelineEntry{}) {
		t.Fatalf("lease renewal = %#v, %v, %v", entry, ok, err)
	}
}

func TestTimelineEntryFromTransitionRejectsUnsafeStateJump(t *testing.T) {
	now := time.Unix(100, 0)
	before := job.Job{ID: "job-1", Status: job.StatusQueued, Attempt: 0, MaxAttempts: 3, UpdatedAt: now, Version: 1}
	after := before
	after.Status = job.StatusSucceeded
	after.Version = 2
	after.UpdatedAt = now.Add(time.Second)
	if _, _, err := job.TimelineEntryFromTransition(&before, after); !errors.Is(err, job.ErrInvalidTimelineEntry) {
		t.Fatalf("unsafe jump error = %v", err)
	}
}
