package job_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"control-center/internal/orchestration/job"
)

func TestManualRetryHistoryEvidenceTracksRootAndUsedBudget(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 7, 0, 0, 0, time.UTC)
	jobs := job.NewMemoryRepository()
	root := failedManualRetrySource(t, jobs, now, "job-history-root")
	repository, _ := job.NewMemoryManualRetryRepository(jobs)

	empty, err := job.BuildManualRetryHistoryEvidence(ctx, repository, root, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if empty.RootJobID != root.ID || empty.UsedManualRetries != 0 || empty.SourceAlreadyRetried || empty.Digest == "" {
		t.Fatalf("unexpected empty history evidence: %#v", empty)
	}

	request := manualRetryRequest(root, "job-history-child", now.Add(4*time.Second))
	child, _, created, err := repository.CreateManualRetry(ctx, request)
	if err != nil || !created {
		t.Fatalf("create retry child=%#v created=%v err=%v", child, created, err)
	}
	rootHistory, err := job.BuildManualRetryHistoryEvidence(ctx, repository, root, now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if rootHistory.UsedManualRetries != 1 || !rootHistory.SourceAlreadyRetried || rootHistory.RootJobID != root.ID || rootHistory.Digest == empty.Digest {
		t.Fatalf("root retry history did not advance: before=%#v after=%#v", empty, rootHistory)
	}

	childHistory, err := job.BuildManualRetryHistoryEvidence(ctx, repository, child, now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if childHistory.RootJobID != root.ID || childHistory.UsedManualRetries != 1 || childHistory.SourceAlreadyRetried {
		t.Fatalf("child retry history evidence is incorrect: %#v", childHistory)
	}
	if childHistory.Digest != rootHistory.Digest {
		t.Fatalf("same lineage produced different history digest: root=%q child=%q", rootHistory.Digest, childHistory.Digest)
	}
}

func TestManualRetryHistoryEvidenceIsDeterministicAcrossObservationTimes(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 7, 5, 0, 0, time.UTC)
	jobs := job.NewMemoryRepository()
	root := failedManualRetrySource(t, jobs, now, "job-history-deterministic")
	repository, _ := job.NewMemoryManualRetryRepository(jobs)
	if _, _, _, err := repository.CreateManualRetry(ctx, manualRetryRequest(root, "job-history-deterministic-child", now.Add(3*time.Second))); err != nil {
		t.Fatal(err)
	}
	first, err := job.BuildManualRetryHistoryEvidence(ctx, repository, root, now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	second, err := job.BuildManualRetryHistoryEvidence(ctx, repository, root, now.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest || first.UsedManualRetries != second.UsedManualRetries {
		t.Fatalf("observation time changed history identity: first=%#v second=%#v", first, second)
	}
	if first.ObservedAt.Equal(second.ObservedAt) {
		t.Fatalf("fresh observation time was not preserved: first=%v second=%v", first.ObservedAt, second.ObservedAt)
	}
}

func TestManualRetryHistoryEvidenceRequiresFreshObservation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 7, 10, 0, 0, time.UTC)
	jobs := job.NewMemoryRepository()
	root := failedManualRetrySource(t, jobs, now, "job-history-stale-observation")
	repository, _ := job.NewMemoryManualRetryRepository(jobs)
	if _, err := job.BuildManualRetryHistoryEvidence(ctx, repository, root, root.UpdatedAt.Add(-time.Second)); !errors.Is(err, job.ErrInvalidManualRetryHistory) {
		t.Fatalf("stale observation error=%v, want ErrInvalidManualRetryHistory", err)
	}
}
