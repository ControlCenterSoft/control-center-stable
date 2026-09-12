package operationsview

import (
	"context"
	"errors"
	"testing"
	"time"

	"control-center/internal/orchestration/job"
)

func TestCreateManualRetryWithRevalidationReplaysCommittedLineageWithoutEvidenceProvider(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 6, 30, 0, 0, time.UTC)
	jobs := job.NewMemoryRepository()
	source := failedRetryMutationSource(t, jobs, now)
	repository, _ := job.NewMemoryManualRetryRepository(jobs)
	reviewed, evidenceSource := reviewedRetryMutationEvidence(t, source, now)
	evidenceSource.policy.ObservedAt = now.Add(6 * time.Second)
	evidenceSource.history.ObservedAt = now.Add(7 * time.Second)
	request := JobRetryMutationRequest{
		Reviewed:            reviewed,
		RetryJobID:          "job-replay-after-commit",
		RetryIdempotencyKey: "replay-after-commit-key",
		RequestedAt:         now.Add(8 * time.Second),
	}

	first, firstLineage, created, err := CreateManualRetryWithRevalidation(ctx, repository, evidenceSource, request)
	if err != nil || !created {
		t.Fatalf("first mutation = %#v lineage=%#v created=%v err=%v", first, firstLineage, created, err)
	}

	// Simulate a lost response: by the time the client retries, authoritative
	// policy/history may already count the committed retry. The committed lineage
	// must be returned without requiring those mutable providers again.
	request.RequestedAt = request.RequestedAt.Add(time.Minute)
	replayed, replayedLineage, created, err := CreateManualRetryWithRevalidation(ctx, repository, nil, request)
	if err != nil || created {
		t.Fatalf("replay mutation = %#v lineage=%#v created=%v err=%v", replayed, replayedLineage, created, err)
	}
	if replayed.ID != first.ID || replayed.Version != first.Version || replayedLineage != firstLineage {
		t.Fatalf("committed replay changed identity: first=%#v/%#v replay=%#v/%#v", first, firstLineage, replayed, replayedLineage)
	}
}

func TestCreateManualRetryWithRevalidationRejectsDifferentChildOnCommittedAdmission(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 6, 35, 0, 0, time.UTC)
	jobs := job.NewMemoryRepository()
	source := failedRetryMutationSource(t, jobs, now)
	repository, _ := job.NewMemoryManualRetryRepository(jobs)
	reviewed, evidenceSource := reviewedRetryMutationEvidence(t, source, now)
	evidenceSource.policy.ObservedAt = now.Add(6 * time.Second)
	evidenceSource.history.ObservedAt = now.Add(7 * time.Second)

	request := JobRetryMutationRequest{
		Reviewed:            reviewed,
		RetryJobID:          "job-original-child",
		RetryIdempotencyKey: "original-child-key",
		RequestedAt:         now.Add(8 * time.Second),
	}
	if _, _, created, err := CreateManualRetryWithRevalidation(ctx, repository, evidenceSource, request); err != nil || !created {
		t.Fatalf("initial mutation created=%v err=%v", created, err)
	}

	request.RetryJobID = "job-different-child"
	request.RetryIdempotencyKey = "different-child-key"
	request.RequestedAt = request.RequestedAt.Add(time.Minute)
	if _, _, _, err := CreateManualRetryWithRevalidation(ctx, repository, nil, request); !errors.Is(err, job.ErrManualRetryAdmissionConflict) {
		t.Fatalf("changed committed child error = %v, want ErrManualRetryAdmissionConflict", err)
	}
}
