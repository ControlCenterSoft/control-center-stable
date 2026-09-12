package job_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"control-center/internal/orchestration/job"
)

func TestMemoryManualRetryAllowsOnlyOneChildPerExactSourceVersion(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 6, 40, 0, 0, time.UTC)
	jobs := job.NewMemoryRepository()
	source := failedManualRetrySource(t, jobs, now, "job-single-child-source")
	repository, _ := job.NewMemoryManualRetryRepository(jobs)

	first := manualRetryRequest(source, "job-single-child-a", now.Add(3*time.Second))
	if _, _, created, err := repository.CreateManualRetry(ctx, first); err != nil || !created {
		t.Fatalf("first retry created=%v err=%v", created, err)
	}

	second := manualRetryRequest(source, "job-single-child-b", now.Add(4*time.Second))
	second.ReviewedAdmissionID = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	second.RevalidationAdmissionID = "sha256:4444444444444444444444444444444444444444444444444444444444444444"
	if _, _, _, err := repository.CreateManualRetry(ctx, second); !errors.Is(err, job.ErrManualRetrySourceAlreadyRetried) {
		t.Fatalf("second lineage from same source version error = %v, want ErrManualRetrySourceAlreadyRetried", err)
	}
	if _, err := jobs.Get(ctx, second.RetryJobID); !errors.Is(err, job.ErrNotFound) {
		t.Fatalf("duplicate source retry created unexpected child: %v", err)
	}
}
