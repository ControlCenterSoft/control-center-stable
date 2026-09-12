package job_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"control-center/internal/orchestration/events"
	"control-center/internal/orchestration/job"
)

const (
	manualRetryDigestA = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	manualRetryDigestB = "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
)

func failedManualRetrySource(t *testing.T, jobs *job.MemoryRepository, now time.Time, id string) job.Job {
	t.Helper()
	created, wasCreated, err := jobs.Create(context.Background(), job.CreateRequest{
		ID:             id,
		ChangeID:       "change-" + id,
		ActionName:     "node.update",
		Input:          json.RawMessage(`{"target":"node-1","credential":"must-remain-private"}`),
		IdempotencyKey: "source-key-" + id,
		MaxAttempts:    1,
		Now:            now,
	})
	if err != nil || !wasCreated {
		t.Fatalf("create source = %#v created=%v err=%v", created, wasCreated, err)
	}
	claimed, ok, err := jobs.Claim(context.Background(), "worker-a", now.Add(time.Second), time.Minute)
	if err != nil || !ok || claimed.ID != id {
		t.Fatalf("claim source = %#v ok=%v err=%v", claimed, ok, err)
	}
	failed, err := jobs.Fail(context.Background(), id, claimed.Lease.Token, "private failure detail", events.Output{}, job.RetryPolicy{}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != job.StatusFailed || failed.Version == 0 {
		t.Fatalf("source did not reach failed terminal state: %#v", failed)
	}
	return failed
}

func manualRetryRequest(source job.Job, retryID string, now time.Time) job.ManualRetryRequest {
	return job.ManualRetryRequest{
		SourceJobID:             source.ID,
		ExpectedSourceVersion:   source.Version,
		RetryJobID:              retryID,
		RetryIdempotencyKey:     "retry-key-" + retryID,
		ReviewedAdmissionID:     manualRetryDigestA,
		RevalidationAdmissionID: manualRetryDigestB,
		RevisionID:              "revision-1",
		RevisionDigest:          manualRetryDigestA,
		PolicyID:                "retry-policy-1",
		PolicyDigest:            manualRetryDigestB,
		RetryHistoryDigest:      manualRetryDigestA,
		RequestedAt:             now,
	}
}

func TestMemoryManualRetryCreatesFreshLineageWithoutMutatingSource(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 5, 30, 0, 0, time.UTC)
	jobs := job.NewMemoryRepository()
	source := failedManualRetrySource(t, jobs, now, "job-source")
	repository, err := job.NewMemoryManualRetryRepository(jobs)
	if err != nil {
		t.Fatal(err)
	}

	retry, lineage, created, err := repository.CreateManualRetry(ctx, manualRetryRequest(source, "job-retry-1", now.Add(3*time.Second)))
	if err != nil || !created {
		t.Fatalf("manual retry = %#v lineage=%#v created=%v err=%v", retry, lineage, created, err)
	}
	if retry.ID != "job-retry-1" || retry.Status != job.StatusQueued || retry.Version != 1 || retry.Attempt != 0 {
		t.Fatalf("unexpected fresh retry job: %#v", retry)
	}
	if retry.ChangeID != source.ChangeID || retry.ActionName != source.ActionName || string(retry.Input) != string(source.Input) {
		t.Fatalf("retry job lost immutable source workload binding: %#v", retry)
	}
	if lineage.RootJobID != source.ID || lineage.SourceJobID != source.ID || lineage.SourceJobVersion != source.Version || lineage.RetryJobID != retry.ID {
		t.Fatalf("unexpected retry lineage: %#v", lineage)
	}
	unchanged, err := jobs.Get(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != source.Status || unchanged.Version != source.Version || unchanged.LastError != source.LastError {
		t.Fatalf("source job was mutated by manual retry: before=%#v after=%#v", source, unchanged)
	}
}

func TestMemoryManualRetryExactAdmissionReplayIsIdempotent(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 5, 35, 0, 0, time.UTC)
	jobs := job.NewMemoryRepository()
	source := failedManualRetrySource(t, jobs, now, "job-replay-source")
	repository, _ := job.NewMemoryManualRetryRepository(jobs)
	request := manualRetryRequest(source, "job-replay-child", now.Add(3*time.Second))

	first, firstLineage, created, err := repository.CreateManualRetry(ctx, request)
	if err != nil || !created {
		t.Fatalf("first retry created=%v err=%v", created, err)
	}
	request.RequestedAt = request.RequestedAt.Add(time.Minute)
	second, secondLineage, created, err := repository.CreateManualRetry(ctx, request)
	if err != nil || created {
		t.Fatalf("replay created=%v err=%v", created, err)
	}
	if second.ID != first.ID || second.Version != first.Version || secondLineage != firstLineage {
		t.Fatalf("replay returned different lineage: first=%#v/%#v second=%#v/%#v", first, firstLineage, second, secondLineage)
	}
}

func TestMemoryManualRetryRejectsAdmissionReuseForDifferentLineage(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 5, 40, 0, 0, time.UTC)
	jobs := job.NewMemoryRepository()
	source := failedManualRetrySource(t, jobs, now, "job-conflict-source")
	repository, _ := job.NewMemoryManualRetryRepository(jobs)
	first := manualRetryRequest(source, "job-conflict-child-a", now.Add(3*time.Second))
	if _, _, _, err := repository.CreateManualRetry(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.RetryJobID = "job-conflict-child-b"
	second.RetryIdempotencyKey = "retry-key-job-conflict-child-b"
	if _, _, _, err := repository.CreateManualRetry(ctx, second); !errors.Is(err, job.ErrManualRetryAdmissionConflict) {
		t.Fatalf("admission reuse error = %v, want ErrManualRetryAdmissionConflict", err)
	}
}

func TestMemoryManualRetryRejectsStaleOrNonFailedSource(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 5, 45, 0, 0, time.UTC)
	jobs := job.NewMemoryRepository()
	source := failedManualRetrySource(t, jobs, now, "job-stale-source")
	repository, _ := job.NewMemoryManualRetryRepository(jobs)
	stale := manualRetryRequest(source, "job-stale-child", now.Add(3*time.Second))
	stale.ExpectedSourceVersion++
	if _, _, _, err := repository.CreateManualRetry(ctx, stale); !errors.Is(err, job.ErrVersionConflict) {
		t.Fatalf("stale source error = %v, want ErrVersionConflict", err)
	}

	queued, _, err := jobs.Create(ctx, job.CreateRequest{
		ID:             "job-queued-source",
		ChangeID:       "change-queued-source",
		ActionName:     "node.update",
		Input:          json.RawMessage(`{}`),
		IdempotencyKey: "queued-source-key",
		MaxAttempts:    1,
		Now:            now.Add(10 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repository.CreateManualRetry(ctx, manualRetryRequest(queued, "job-queued-child", now.Add(11*time.Second))); !errors.Is(err, job.ErrManualRetrySourceNotFailed) {
		t.Fatalf("non-failed source error = %v, want ErrManualRetrySourceNotFailed", err)
	}
}

func TestMemoryManualRetryPreservesRootAcrossGenerations(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 5, 50, 0, 0, time.UTC)
	jobs := job.NewMemoryRepository()
	root := failedManualRetrySource(t, jobs, now, "job-root")
	repository, _ := job.NewMemoryManualRetryRepository(jobs)

	firstRequest := manualRetryRequest(root, "job-generation-1", now.Add(3*time.Second))
	first, firstLineage, _, err := repository.CreateManualRetry(ctx, firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := jobs.Claim(ctx, "worker-generation", now.Add(4*time.Second), time.Minute)
	if err != nil || !ok || claimed.ID != first.ID {
		t.Fatalf("claim retry = %#v ok=%v err=%v", claimed, ok, err)
	}
	failedFirst, err := jobs.Fail(ctx, first.ID, claimed.Lease.Token, "second failure", events.Output{}, job.RetryPolicy{}, now.Add(5*time.Second))
	if err != nil || failedFirst.Status != job.StatusFailed {
		t.Fatalf("fail retry = %#v err=%v", failedFirst, err)
	}

	secondRequest := manualRetryRequest(failedFirst, "job-generation-2", now.Add(6*time.Second))
	secondRequest.ReviewedAdmissionID = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	secondRequest.RevalidationAdmissionID = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	_, secondLineage, _, err := repository.CreateManualRetry(ctx, secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	if firstLineage.RootJobID != root.ID || secondLineage.RootJobID != root.ID || secondLineage.SourceJobID != failedFirst.ID {
		t.Fatalf("root lineage was not preserved: first=%#v second=%#v", firstLineage, secondLineage)
	}
	lineage, err := repository.ListManualRetryLineage(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(lineage) != 2 || lineage[0].RetryJobID != first.ID || lineage[1].RetryJobID != "job-generation-2" {
		t.Fatalf("unexpected root lineage list: %#v", lineage)
	}
}
