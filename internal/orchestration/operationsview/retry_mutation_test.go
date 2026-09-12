package operationsview

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"control-center/internal/orchestration/events"
	"control-center/internal/orchestration/job"
)

const mutationRetryDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type retryMutationEvidenceSource struct {
	revisionID     string
	revisionDigest string
	policy         JobRetryPolicyEvidence
	history        job.ManualRetryHistoryEvidence
	err            error
}

func (s retryMutationEvidenceSource) CurrentRevision(context.Context, job.Job) (string, string, error) {
	return s.revisionID, s.revisionDigest, s.err
}

func (s retryMutationEvidenceSource) CurrentRetryPolicy(context.Context, job.Job) (JobRetryPolicyEvidence, error) {
	return s.policy, s.err
}

func (s retryMutationEvidenceSource) CurrentRetryHistory(context.Context, job.Job) (job.ManualRetryHistoryEvidence, error) {
	return s.history, s.err
}

func failedRetryMutationSource(t *testing.T, jobs *job.MemoryRepository, now time.Time) job.Job {
	t.Helper()
	created, _, err := jobs.Create(context.Background(), job.CreateRequest{
		ID:             "job-mutation-source",
		ChangeID:       "change-mutation-source",
		ActionName:     "node.update",
		Input:          json.RawMessage(`{"secret":"must-not-project"}`),
		IdempotencyKey: "source-mutation-key",
		MaxAttempts:    1,
		Now:            now,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := jobs.Claim(context.Background(), "worker-a", now.Add(time.Second), time.Minute)
	if err != nil || !ok || claimed.ID != created.ID {
		t.Fatalf("claim = %#v ok=%v err=%v", claimed, ok, err)
	}
	failed, err := jobs.Fail(context.Background(), claimed.ID, claimed.Lease.Token, "private failure", events.Output{}, job.RetryPolicy{}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return failed
}

func reviewedRetryMutationEvidence(t *testing.T, source job.Job, now time.Time) (JobRetryAdmissionEvidence, retryMutationEvidenceSource) {
	t.Helper()
	policy := JobRetryPolicyEvidence{
		PolicyID:          "retry-policy-v1",
		PolicyDigest:      mutationRetryDigest,
		AllowsManualRetry: true,
		UsedManualRetries: 0,
		MaxManualRetries:  2,
		ObservedAt:        now.Add(3 * time.Second),
	}
	history := job.ManualRetryHistoryEvidence{
		ContractVersion:      job.ManualRetryHistoryContractVersion,
		RootJobID:            source.ID,
		SourceJobID:          source.ID,
		SourceJobVersion:     source.Version,
		UsedManualRetries:    0,
		SourceAlreadyRetried: false,
		Digest:               mutationRetryDigest,
		ObservedAt:           now.Add(4 * time.Second),
	}
	evidenceSource := retryMutationEvidenceSource{
		revisionID:     "revision-mutation-1",
		revisionDigest: mutationRetryDigest,
		policy:         policy,
		history:        history,
	}
	reviewed, err := BuildJobRetryAdmissionEvidence(JobRetryAdmissionInput{
		Job:                    source,
		ExpectedJobVersion:     source.Version,
		RevisionID:             evidenceSource.revisionID,
		RevisionDigest:         evidenceSource.revisionDigest,
		Policy:                 policy,
		RetryHistoryDigest:     history.Digest,
		RetryHistoryObservedAt: history.ObservedAt,
		SourceAlreadyRetried:   history.SourceAlreadyRetried,
		ObservedAt:             now.Add(5 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	return reviewed, evidenceSource
}

func TestCreateManualRetryWithRevalidationCreatesFreshQueuedLineage(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 6, 0, 0, 0, time.UTC)
	jobs := job.NewMemoryRepository()
	source := failedRetryMutationSource(t, jobs, now)
	repository, err := job.NewMemoryManualRetryRepository(jobs)
	if err != nil {
		t.Fatal(err)
	}
	reviewed, evidenceSource := reviewedRetryMutationEvidence(t, source, now)
	// The mutation re-reads fresher evidence. Timestamps may advance while the
	// semantic revision/policy/history boundary remains identical.
	evidenceSource.policy.ObservedAt = now.Add(6 * time.Second)
	evidenceSource.history.ObservedAt = now.Add(7 * time.Second)

	retry, lineage, created, err := CreateManualRetryWithRevalidation(ctx, repository, evidenceSource, JobRetryMutationRequest{
		Reviewed:            reviewed,
		RetryJobID:          "job-mutation-retry-1",
		RetryIdempotencyKey: "mutation-retry-key-1",
		RequestedAt:         now.Add(8 * time.Second),
	})
	if err != nil || !created {
		t.Fatalf("manual retry mutation = %#v lineage=%#v created=%v err=%v", retry, lineage, created, err)
	}
	if retry.Status != job.StatusQueued || retry.Version != 1 || retry.ID != "job-mutation-retry-1" {
		t.Fatalf("unexpected retry job: %#v", retry)
	}
	if lineage.ReviewedAdmissionID != reviewed.AdmissionID || lineage.RevalidationAdmissionID == "" || lineage.RevalidationAdmissionID == reviewed.AdmissionID {
		t.Fatalf("review/revalidation identities not preserved: %#v", lineage)
	}
	unchanged, err := jobs.Get(ctx, source.ID)
	if err != nil || unchanged.Version != source.Version || unchanged.Status != job.StatusFailed {
		t.Fatalf("source changed during retry mutation: %#v err=%v", unchanged, err)
	}
}

func TestCreateManualRetryWithRevalidationRejectsChangedPolicyOrHistory(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 6, 5, 0, 0, time.UTC)
	jobs := job.NewMemoryRepository()
	source := failedRetryMutationSource(t, jobs, now)
	repository, _ := job.NewMemoryManualRetryRepository(jobs)
	reviewed, evidenceSource := reviewedRetryMutationEvidence(t, source, now)
	evidenceSource.policy.ObservedAt = now.Add(6 * time.Second)
	evidenceSource.history.ObservedAt = now.Add(7 * time.Second)
	evidenceSource.policy.PolicyDigest = "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

	_, _, _, err := CreateManualRetryWithRevalidation(ctx, repository, evidenceSource, JobRetryMutationRequest{
		Reviewed:            reviewed,
		RetryJobID:          "job-policy-stale-child",
		RetryIdempotencyKey: "policy-stale-key",
		RequestedAt:         now.Add(8 * time.Second),
	})
	if !errors.Is(err, ErrJobRetryAdmissionStale) {
		t.Fatalf("policy change error = %v, want ErrJobRetryAdmissionStale", err)
	}
	if _, getErr := jobs.Get(ctx, "job-policy-stale-child"); !errors.Is(getErr, job.ErrNotFound) {
		t.Fatalf("stale policy created a child job: %v", getErr)
	}

	evidenceSource.policy.PolicyDigest = reviewed.PolicyDigest
	evidenceSource.history.Digest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	_, _, _, err = CreateManualRetryWithRevalidation(ctx, repository, evidenceSource, JobRetryMutationRequest{
		Reviewed:            reviewed,
		RetryJobID:          "job-history-stale-child",
		RetryIdempotencyKey: "history-stale-key",
		RequestedAt:         now.Add(9 * time.Second),
	})
	if !errors.Is(err, ErrJobRetryAdmissionStale) {
		t.Fatalf("history change error = %v, want ErrJobRetryAdmissionStale", err)
	}
}

func TestCreateManualRetryWithRevalidationRejectsBlockedCurrentEvidence(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 6, 10, 0, 0, time.UTC)
	jobs := job.NewMemoryRepository()
	source := failedRetryMutationSource(t, jobs, now)
	repository, _ := job.NewMemoryManualRetryRepository(jobs)
	reviewed, evidenceSource := reviewedRetryMutationEvidence(t, source, now)
	evidenceSource.policy.ObservedAt = now.Add(6 * time.Second)
	evidenceSource.history.ObservedAt = now.Add(7 * time.Second)
	evidenceSource.policy.UsedManualRetries = evidenceSource.policy.MaxManualRetries
	evidenceSource.history.UsedManualRetries = evidenceSource.policy.MaxManualRetries

	_, _, _, err := CreateManualRetryWithRevalidation(ctx, repository, evidenceSource, JobRetryMutationRequest{
		Reviewed:            reviewed,
		RetryJobID:          "job-budget-blocked-child",
		RetryIdempotencyKey: "budget-blocked-key",
		RequestedAt:         now.Add(8 * time.Second),
	})
	if !errors.Is(err, ErrJobRetryAdmissionStale) {
		t.Fatalf("budget exhaustion error = %v, want ErrJobRetryAdmissionStale", err)
	}
}

func TestCreateManualRetryWithRevalidationRejectsTamperedReviewedEvidence(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 6, 15, 0, 0, time.UTC)
	jobs := job.NewMemoryRepository()
	source := failedRetryMutationSource(t, jobs, now)
	repository, _ := job.NewMemoryManualRetryRepository(jobs)
	reviewed, evidenceSource := reviewedRetryMutationEvidence(t, source, now)
	reviewed.PolicyID = "tampered-policy"

	_, _, _, err := CreateManualRetryWithRevalidation(ctx, repository, evidenceSource, JobRetryMutationRequest{
		Reviewed:            reviewed,
		RetryJobID:          "job-tampered-child",
		RetryIdempotencyKey: "tampered-key",
		RequestedAt:         now.Add(8 * time.Second),
	})
	if !errors.Is(err, ErrJobRetryAdmissionTampered) {
		t.Fatalf("tampered evidence error = %v, want ErrJobRetryAdmissionTampered", err)
	}
}

func TestCreateManualRetryWithRevalidationRejectsSourceVersionAdvance(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 6, 20, 0, 0, time.UTC)
	jobs := job.NewMemoryRepository()
	source := failedRetryMutationSource(t, jobs, now)
	repository, _ := job.NewMemoryManualRetryRepository(jobs)
	_, evidenceSource := reviewedRetryMutationEvidence(t, source, now)

	// Creating the first retry from the source leaves the source unchanged, so
	// use an explicit current-version mismatch in the reviewed evidence while
	// keeping its integrity digest valid by rebuilding a new reviewed snapshot.
	advanced := source
	advanced.Version++
	advanced.UpdatedAt = now.Add(6 * time.Second)
	staleReviewed, err := BuildJobRetryAdmissionEvidence(JobRetryAdmissionInput{
		Job:                advanced,
		ExpectedJobVersion: advanced.Version,
		RevisionID:         evidenceSource.revisionID,
		RevisionDigest:     evidenceSource.revisionDigest,
		Policy: JobRetryPolicyEvidence{
			PolicyID:          evidenceSource.policy.PolicyID,
			PolicyDigest:      evidenceSource.policy.PolicyDigest,
			AllowsManualRetry: true,
			UsedManualRetries: 0,
			MaxManualRetries:  2,
			ObservedAt:        now.Add(7 * time.Second),
		},
		RetryHistoryDigest:     evidenceSource.history.Digest,
		RetryHistoryObservedAt: now.Add(7 * time.Second),
		ObservedAt:             now.Add(8 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, _, _, err = CreateManualRetryWithRevalidation(ctx, repository, evidenceSource, JobRetryMutationRequest{
		Reviewed:            staleReviewed,
		RetryJobID:          "job-version-stale-child",
		RetryIdempotencyKey: "version-stale-key",
		RequestedAt:         now.Add(9 * time.Second),
	})
	if !errors.Is(err, ErrJobRetryAdmissionStale) {
		t.Fatalf("source version error = %v, want ErrJobRetryAdmissionStale", err)
	}
}

func TestCreateManualRetryWithRevalidationRejectsPolicyHistoryUsageMismatch(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 6, 25, 0, 0, time.UTC)
	jobs := job.NewMemoryRepository()
	source := failedRetryMutationSource(t, jobs, now)
	repository, _ := job.NewMemoryManualRetryRepository(jobs)
	reviewed, evidenceSource := reviewedRetryMutationEvidence(t, source, now)
	evidenceSource.policy.ObservedAt = now.Add(6 * time.Second)
	evidenceSource.history.ObservedAt = now.Add(7 * time.Second)
	evidenceSource.policy.UsedManualRetries = 1

	_, _, _, err := CreateManualRetryWithRevalidation(ctx, repository, evidenceSource, JobRetryMutationRequest{
		Reviewed:            reviewed,
		RetryJobID:          "job-usage-mismatch-child",
		RetryIdempotencyKey: "usage-mismatch-key",
		RequestedAt:         now.Add(8 * time.Second),
	})
	if !errors.Is(err, ErrJobRetryAdmissionStale) {
		t.Fatalf("usage mismatch error = %v, want ErrJobRetryAdmissionStale", err)
	}
}
